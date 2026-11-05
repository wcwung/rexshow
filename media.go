package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// encoded is RGBA pixels, zlib-compressed, as the kitty protocol takes them
// (f=32, o=z).
type encoded struct {
	data []byte
	w, h int
}

func encodeRGBA(pix []byte, w, h int) encoded {
	var buf bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&buf, zlib.BestSpeed)
	zw.Write(pix)
	zw.Close()
	return encoded{buf.Bytes(), w, h}
}

// fit is the largest size with the aspect of w x h inside maxW x maxH. Small
// images are not enlarged here; the terminal scales them up to their cells.
func fit(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 || maxW <= 0 || maxH <= 0 {
		return 1, 1
	}
	s := min(float64(maxW)/float64(w), float64(maxH)/float64(h), 1)
	return max(1, int(float64(w)*s+0.5)), max(1, int(float64(h)*s+0.5))
}

func decodeFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(bufio.NewReader(f))
	return src, err
}

func scaleTo(src image.Image, w, h int) encoded {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	scaler := draw.Scaler(draw.CatmullRom)
	if b.Dx()*b.Dy() > 4_000_000 {
		scaler = draw.ApproxBiLinear
	}
	scaler.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return encodeRGBA(dst.Pix, w, h)
}

func loadImage(path string, maxW, maxH int) (encoded, error) {
	src, err := decodeFile(path)
	if err != nil {
		return encoded{}, err
	}
	b := src.Bounds()
	w, h := fit(b.Dx(), b.Dy(), maxW, maxH)
	return scaleTo(src, w, h), nil
}

// loadStill decodes path one time into the full image, up to maxW x maxH.
// When the full image has more than four times the pixels of the view, it
// also makes a copy at the view's size, which is much less data to send.
// A smaller image gets no copy: scaled down, it compresses less well.
func loadStill(path string, maxW, maxH, viewW, viewH int) (full, fitted encoded, err error) {
	src, err := decodeFile(path)
	if err != nil {
		return encoded{}, encoded{}, err
	}
	b := src.Bounds()
	w, h := fit(b.Dx(), b.Dy(), maxW, maxH)
	full = scaleTo(src, w, h)
	if fw, fh := fit(w, h, viewW, viewH); w*h > 4*fw*fh {
		fitted = scaleTo(src, fw, fh)
	}
	return full, fitted, nil
}

type videoInfo struct {
	w, h     int
	duration float64 // seconds
}

func probeVideo(path string) (videoInfo, error) {
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height:format=duration", "-of", "json", path).Output()
	if err != nil {
		return videoInfo{}, fmt.Errorf("ffprobe: %w", err)
	}
	var r struct {
		Streams []struct{ Width, Height int } `json:"streams"`
		Format  struct{ Duration string }     `json:"format"`
	}
	if err := json.Unmarshal(out, &r); err != nil || len(r.Streams) == 0 {
		return videoInfo{}, fmt.Errorf("ffprobe: no video stream in %s", path)
	}
	d, _ := strconv.ParseFloat(r.Format.Duration, 64)
	return videoInfo{r.Streams[0].Width, r.Streams[0].Height, d}, nil
}

// posterFrame is one frame of the video, about a second in or from the
// start of a shorter clip.
func posterFrame(path string, maxW, maxH int) (encoded, error) {
	info, err := probeVideo(path)
	if err != nil {
		return encoded{}, err
	}
	w, h := fit(info.w, info.h, maxW, maxH)
	w, h = w&^1, h&^1
	at := "0"
	if info.duration > 2 {
		at = "1"
	}
	out, err := exec.Command("ffmpeg", "-v", "error", "-ss", at, "-i", path, "-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d", w, h), "-f", "rawvideo", "-pix_fmt", "rgba", "-").Output()
	if err != nil || len(out) < w*h*4 {
		return encoded{}, fmt.Errorf("ffmpeg poster frame: %v", err)
	}
	return encodeRGBA(out[:w*h*4], w, h), nil
}

// clip is a video decoded in the background into compressed frames, so the
// player can loop, pause, and step back without decoding again.
type clip struct {
	path   string
	fps    float64
	w, h   int
	cancel context.CancelFunc

	mu     sync.Mutex
	frames []encoded
	done   bool
	err    error
}

const (
	clipFPS       = 12
	clipMaxFrames = 3600 // five minutes at 12 fps
)

func decodeClip(path string, maxW, maxH int, notify func()) (*clip, error) {
	info, err := probeVideo(path)
	if err != nil {
		return nil, err
	}
	w, h := fit(info.w, info.h, maxW, maxH)
	w, h = max(2, w&^1), max(2, h&^1)
	ctx, cancel := context.WithCancel(context.Background())
	c := &clip{path: path, fps: clipFPS, w: w, h: h, cancel: cancel}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", path,
		"-vf", fmt.Sprintf("fps=%d,scale=%d:%d", clipFPS, w, h),
		"-frames:v", strconv.Itoa(clipMaxFrames), "-f", "rawvideo", "-pix_fmt", "rgba", "-")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	go func() {
		frame := make([]byte, w*h*4)
		prev := make([]byte, len(frame))
		var last encoded
		r := bufio.NewReaderSize(out, len(frame))
		for {
			if _, err := io.ReadFull(r, frame); err != nil {
				break
			}
			// Screen recordings hold many frames with no change. Such a frame
			// shares the data of the one before it, so the player sends it once.
			e := last
			if last.data == nil || !bytes.Equal(frame, prev) {
				e = encodeRGBA(frame, w, h)
				prev, frame = frame, prev
				last = e
			}
			c.mu.Lock()
			c.frames = append(c.frames, e)
			first := len(c.frames) == 1
			c.mu.Unlock()
			if first {
				notify()
			}
		}
		err := cmd.Wait()
		c.mu.Lock()
		c.done = true
		if len(c.frames) == 0 && ctx.Err() == nil {
			c.err = fmt.Errorf("ffmpeg: %v %s", err, strings.TrimSpace(stderr.String()))
		}
		c.mu.Unlock()
		notify()
	}()
	return c, nil
}

func (c *clip) frame(i int) (encoded, int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.frames) == 0 {
		return encoded{}, 0, c.done
	}
	return c.frames[min(i, len(c.frames)-1)], len(c.frames), c.done
}

func (c *clip) stop() { c.cancel() }
