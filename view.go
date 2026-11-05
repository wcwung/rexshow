package main

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type winSize struct{ rows, cols, xpix, ypix int }

func getWinSize(fd int) winSize {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil || ws.Row == 0 || ws.Col == 0 {
		return winSize{rows: 24, cols: 80, xpix: 80 * 16, ypix: 24 * 34}
	}
	w := winSize{rows: int(ws.Row), cols: int(ws.Col), xpix: int(ws.Xpixel), ypix: int(ws.Ypixel)}
	if w.xpix == 0 || w.ypix == 0 {
		w.xpix, w.ypix = w.cols*16, w.rows*34
	}
	return w
}

func (ws winSize) cell() (float64, float64) {
	return float64(ws.xpix) / float64(ws.cols), float64(ws.ypix) / float64(ws.rows)
}

// --- kitty graphics ---------------------------------------------------------

const (
	idStill  = 1
	idFrameA = 2
	idFrameB = 3
	idThumb0 = 10
)

func kittyUpload(w *bufio.Writer, e encoded, id int) {
	data := base64.StdEncoding.EncodeToString(e.data)
	const chunk = 4096
	for i := 0; i < len(data); i += chunk {
		end := min(i+chunk, len(data))
		more := 0
		if end < len(data) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(w, "\x1b_Ga=t,f=32,o=z,s=%d,v=%d,i=%d,q=2,m=%d;%s\x1b\\", e.w, e.h, id, more, data[i:end])
		} else {
			fmt.Fprintf(w, "\x1b_Gm=%d;%s\x1b\\", more, data[i:end])
		}
	}
}

// kittyPlace shows image id stretched over cols x rows cells with its top
// left at cell (row, col). C=1 leaves the cursor alone.
func kittyPlace(w *bufio.Writer, id int, b box) {
	fmt.Fprintf(w, "\x1b7\x1b[%d;%dH\x1b_Ga=p,i=%d,p=1,c=%d,r=%d,C=1,q=2\x1b\\\x1b8", b.row, b.col, id, b.cols, b.rows)
}

func kittyDelete(w *bufio.Writer, id int) {
	fmt.Fprintf(w, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id)
}

// kittyPlaceRect shows the src rectangle of image id over the cells of b.
func kittyPlaceRect(w *bufio.Writer, id int, b box, src rect) {
	fmt.Fprintf(w, "\x1b7\x1b[%d;%dH\x1b_Ga=p,i=%d,p=1,x=%d,y=%d,w=%d,h=%d,c=%d,r=%d,C=1,q=2\x1b\\\x1b8",
		b.row, b.col, id, src.x, src.y, src.w, src.h, b.cols, b.rows)
}

// box is a rectangle of cells, 1-based.
type box struct{ row, col, rows, cols int }

// rect is a rectangle of image pixels.
type rect struct{ x, y, w, h int }

// zoom is how far the main image is magnified past "fit", and the point of
// the image at the middle of the view, as fractions of its width and height.
type zoom struct{ level, cx, cy float64 }

var zoomSteps = []float64{1, 1.5, 2, 3, 4, 6, 8}

func (z zoom) step(dir int) float64 {
	if dir > 0 {
		for _, l := range zoomSteps {
			if l > z.level+0.01 {
				return l
			}
		}
		return zoomSteps[len(zoomSteps)-1]
	}
	for i := len(zoomSteps) - 1; i >= 0; i-- {
		if zoomSteps[i] < z.level-0.01 {
			return zoomSteps[i]
		}
	}
	return 1
}

// view picks the part of a w x h image to show in area at zoom z, and the
// cells to show it in. At level 1 it is the whole image, fitted and centered.
func (z zoom) view(area box, w, h int, ws winSize) (box, rect) {
	cw, ch := ws.cell()
	boxW, boxH := float64(area.cols)*cw, float64(area.rows)*ch
	s := min(boxW/float64(w), boxH/float64(h)) * max(1, z.level)
	srcW, srcH := min(float64(w), boxW/s), min(float64(h), boxH/s)
	x0 := min(max(0, z.cx*float64(w)-srcW/2), float64(w)-srcW)
	y0 := min(max(0, z.cy*float64(h)-srcH/2), float64(h)-srcH)
	cols := max(1, min(area.cols, int(srcW*s/cw+0.5)))
	rows := max(1, min(area.rows, int(srcH*s/ch+0.5)))
	b := box{area.row + (area.rows-rows)/2, area.col + (area.cols-cols)/2, rows, cols}
	return b, rect{int(x0), int(y0), max(1, int(srcW)), max(1, int(srcH))}
}

// clamped keeps the view's middle where the view stays inside the image.
func (z zoom) clamped(area box, w, h int, ws winSize) zoom {
	_, r := z.view(area, w, h, ws)
	z.cx = (float64(r.x) + float64(r.w)/2) / float64(w)
	z.cy = (float64(r.y) + float64(r.h)/2) / float64(h)
	return z
}

// zoomAt changes the level and keeps the image point under cell (row, col)
// where it is, so the wheel zooms toward the mouse.
func (z zoom) zoomAt(level float64, row, col int, area box, w, h int, ws winSize) zoom {
	b, r := z.view(area, w, h, ws)
	fx := min(max((float64(col-b.col)+0.5)/float64(b.cols), 0), 1)
	fy := min(max((float64(row-b.row)+0.5)/float64(b.rows), 0), 1)
	px, py := float64(r.x)+fx*float64(r.w), float64(r.y)+fy*float64(r.h)
	next := zoom{level: level, cx: z.cx, cy: z.cy}
	_, nr := next.view(area, w, h, ws)
	next.cx = (px - fx*float64(nr.w) + float64(nr.w)/2) / float64(w)
	next.cy = (py - fy*float64(nr.h) + float64(nr.h)/2) / float64(h)
	return next.clamped(area, w, h, ws)
}

// place centers an image of w x h pixels in area at the largest size that
// fits, in whole cells.
func place(area box, w, h int, ws winSize) box {
	cw, ch := ws.cell()
	maxW, maxH := float64(area.cols)*cw, float64(area.rows)*ch
	s := min(maxW/float64(w), maxH/float64(h))
	cols := max(1, min(area.cols, int(float64(w)*s/cw+0.5)))
	rows := max(1, min(area.rows, int(float64(h)*s/ch+0.5)))
	return box{area.row + (area.rows-rows)/2, area.col + (area.cols-cols)/2, rows, cols}
}

// --- layout ------------------------------------------------------------------

type layout struct {
	ws        winSize
	main      box
	thumbs    []box // slots in the strip, left to right
	markerRow int
	footerRow int
}

const (
	thumbRows = 3
	// Stills keep their full size, up to this, so zooming in shows detail.
	stillMax  = 4096
	videoMaxW = 960
	videoMaxH = 720
)

func makeLayout(ws winSize) layout {
	l := layout{ws: ws, footerRow: ws.rows}
	bottom := ws.rows - 1
	if ws.rows >= 16 && ws.cols >= 24 {
		cw, ch := ws.cell()
		thumbCols := max(4, int(float64(thumbRows)*ch*16/10/cw+0.5))
		count := max(1, (ws.cols+1)/(thumbCols+1))
		used := count*(thumbCols+1) - 1
		left := 1 + (ws.cols-used)/2
		l.markerRow = ws.rows - 1
		top := l.markerRow - thumbRows
		for i := range count {
			l.thumbs = append(l.thumbs, box{top, left + i*(thumbCols+1), thumbRows, thumbCols})
		}
		bottom = top - 2
	}
	l.main = box{3, 1, max(1, bottom-2), ws.cols}
	return l
}

// --- input -------------------------------------------------------------------

type key int

const (
	keyNone key = iota
	keyPrev
	keyNext
	keyLeft
	keyRight
	keyUp
	keyDown
	keyFirst
	keyLast
	keyPlay
	keyStepBack
	keyStep
	keyZoomIn
	keyZoomOut
	keyZoomToggle
	keyZoomReset
	keyWheelIn
	keyWheelOut
	keyTap
	keyQuit
)

// input is a key press, or a wheel tick at a cell.
type input struct {
	key      key
	row, col int
}

func readKeys(keys chan<- input) {
	buf := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			close(keys)
			return
		}
		for _, k := range decodeKeys(buf[:n]) {
			keys <- k
		}
	}
}

func decodeKeys(b []byte) []input {
	var out []input
	add := func(k key) { out = append(out, input{key: k}) }
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == 0x1b && i+2 < len(b) && (b[i+1] == '[' || b[i+1] == 'O') {
			j := i + 2
			for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
				j++
			}
			if j >= len(b) {
				break
			}
			seq := string(b[i+2 : j+1])
			if wheel, row, col, ok := parseWheel(seq); ok {
				if wheel != keyNone {
					out = append(out, input{wheel, row, col})
				}
				i = j
				continue
			}
			switch seq {
			case "D":
				add(keyLeft)
			case "C":
				add(keyRight)
			case "A":
				add(keyUp)
			case "B":
				add(keyDown)
			case "5~":
				add(keyPrev)
			case "6~":
				add(keyNext)
			case "H", "1~", "7~":
				add(keyFirst)
			case "F", "4~", "8~":
				add(keyLast)
			}
			i = j
			continue
		}
		switch c {
		case 'h':
			add(keyLeft)
		case 'l':
			add(keyRight)
		case 'k':
			add(keyUp)
		case 'j':
			add(keyDown)
		case 'p':
			add(keyPrev)
		case 'n':
			add(keyNext)
		case 'g':
			add(keyFirst)
		case 'G':
			add(keyLast)
		case ' ':
			add(keyPlay)
		case ',':
			add(keyStepBack)
		case '.':
			add(keyStep)
		case '+', '=':
			add(keyZoomIn)
		case '-', '_':
			add(keyZoomOut)
		case 'z', '\r':
			add(keyZoomToggle)
		case '0':
			add(keyZoomReset)
		case 'q', 3:
			add(keyQuit)
		}
	}
	return out
}

// parseWheel reads an SGR mouse report (mode 1006): "<0;col;rowM" is a
// click or, from Rex on a phone, a tap; "<64;col;rowM" is the wheel turned
// up, 65 down. Releases ("m") and other buttons are read and dropped.
func parseWheel(seq string) (key, int, int, bool) {
	if !strings.HasPrefix(seq, "<") || !(strings.HasSuffix(seq, "M") || strings.HasSuffix(seq, "m")) {
		return keyNone, 0, 0, false
	}
	if strings.HasSuffix(seq, "m") {
		return keyNone, 0, 0, true
	}
	parts := strings.Split(seq[1:len(seq)-1], ";")
	if len(parts) != 3 {
		return keyNone, 0, 0, false
	}
	btn, _ := strconv.Atoi(parts[0])
	col, _ := strconv.Atoi(parts[1])
	row, _ := strconv.Atoi(parts[2])
	switch btn &^ (4 | 8 | 16) {
	case 0:
		return keyTap, row, col, true
	case 64:
		return keyWheelIn, row, col, true
	case 65:
		return keyWheelOut, row, col, true
	}
	return keyNone, 0, 0, true
}

// --- the viewer ----------------------------------------------------------------

// button is a tap target in the bottom row. Rex on a phone sends taps as
// clicks and nothing else, so every control has one.
type button struct {
	label      string
	key        key
	col0, col1 int
}

type thumbJob struct {
	path       string
	kind       mediaKind
	maxW, maxH int
}

type thumbResult struct {
	key string
	img encoded
	err error
}

func thumbKey(path string, maxW, maxH int) string { return fmt.Sprintf("%s@%dx%d", path, maxW, maxH) }

func runView(name string) error {
	in := int(os.Stdin.Fd())
	if term.IsTerminal(in) {
		if old, err := term.MakeRaw(in); err == nil {
			defer term.Restore(in, old)
		}
	}
	w := bufio.NewWriterSize(os.Stdout, 1<<16)
	fmt.Fprint(w, "\x1b[?1049h\x1b[?25l\x1b[H\x1b[2J\x1b[?1000h\x1b[?1006h")
	w.Flush()
	defer func() {
		fmt.Fprint(w, "\x1b_Ga=d,d=A,q=2\x1b\\\x1b[?1006l\x1b[?1000l\x1b[?25h\x1b[?1049l")
		w.Flush()
	}()

	keys := make(chan input, 64)
	go readKeys(keys)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	wake := make(chan struct{}, 1)
	notify := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}

	jobs := make(chan thumbJob, 256)
	results := make(chan thumbResult, 256)
	go func() {
		for j := range jobs {
			var img encoded
			var err error
			if j.kind == kindVideo {
				img, err = posterFrame(j.path, j.maxW, j.maxH)
			} else {
				img, err = loadImage(j.path, j.maxW, j.maxH)
			}
			results <- thumbResult{thumbKey(j.path, j.maxW, j.maxH), img, err}
		}
	}()
	defer close(jobs)

	var (
		show       = &Show{}
		entries    []Entry
		idx        = -1
		follow     = true
		ws         = getWinSize(in)
		lay        = makeLayout(ws)
		thumbs     = map[string]encoded{}
		pending    = map[string]bool{}
		stillPath  string
		still      encoded
		stillErr   error
		cur        *clip
		playing    = true
		frameIdx   = 0
		frameID    = idFrameA
		shownFrame = -1
		playFrom   time.Time // when playback reached playBase
		playBase   int
		lastStamp  time.Time
		lastScan   time.Time
		signature  string
		zm         = zoom{1, 0.5, 0.5}
		buttons    []button
	)

	reload := func() bool {
		if info, err := os.Stat(showPath(name)); err == nil && info.ModTime() != lastStamp {
			lastStamp = info.ModTime()
			if s, err := loadShow(name); err == nil {
				show = s
			}
		} else if len(show.Dirs) == 0 && signature != "" {
			return false
		}
		lastScan = time.Now()
		next := show.entries()
		var sb strings.Builder
		for _, e := range next {
			sb.WriteString(e.Path + "\x00" + e.Caption + "\x00")
		}
		if sb.String() == signature {
			return false
		}
		signature = sb.String()
		var at string
		if idx >= 0 && idx < len(entries) {
			at = entries[idx].Path
		}
		entries = next
		switch {
		case len(entries) == 0:
			idx = -1
		case follow || at == "":
			idx = len(entries) - 1
		default:
			idx = len(entries) - 1
			for i, e := range entries {
				if e.Path == at {
					idx = i
				}
			}
		}
		return true
	}

	stopClip := func() {
		if cur != nil {
			cur.stop()
			cur = nil
		}
		shownFrame = -1
	}

	// load prepares the current entry: a scaled still, or a clip decoding in
	// the background.
	load := func() {
		if idx < 0 {
			stopClip()
			stillPath = ""
			return
		}
		e := entries[idx]
		cw, ch := ws.cell()
		maxW, maxH := int(float64(lay.main.cols)*cw), int(float64(lay.main.rows)*ch)
		if e.Kind == kindVideo {
			if cur != nil && cur.path == e.Path {
				return
			}
			stopClip()
			stillPath = ""
			frameIdx, playing = 0, true
			playFrom, playBase = time.Now(), 0
			// The terminal scales frames up to the pane; smaller frames keep
			// playback smooth over a slow link.
			cur, stillErr = decodeClip(e.Path, min(maxW, videoMaxW), min(maxH, videoMaxH), notify)
			return
		}
		stopClip()
		if stillPath == e.Path {
			return
		}
		stillPath = e.Path
		still, stillErr = loadImage(e.Path, max(maxW, stillMax), max(maxH, stillMax))
	}

	text := func(row, col int, style, s string, maxCols int) {
		r := []rune(s)
		if maxCols <= 0 {
			return
		}
		if len(r) > maxCols {
			r = append(r[:max(0, maxCols-1)], '…')
		}
		fmt.Fprintf(w, "\x1b[%d;%dH%s%s\x1b[0m", row, col, style, string(r))
	}
	clock := func(sec float64) string {
		s := int(sec)
		return fmt.Sprintf("%d:%02d", s/60, s%60)
	}

	drawFooter := func() {
		fmt.Fprintf(w, "\x1b[%d;1H\x1b[2K", lay.footerRow)
		buttons = buttons[:0]
		if idx < 0 {
			return
		}
		labels := []button{{label: "◀", key: keyPrev}, {label: "▶", key: keyNext},
			{label: "−", key: keyZoomOut}, {label: "+", key: keyZoomIn}, {label: "fit", key: keyZoomReset}}
		if cur != nil {
			play := "❚❚"
			if !playing {
				play = "▶"
			}
			labels = append(labels, button{label: play + " ", key: keyPlay})
		}
		col := 2
		for _, b := range labels {
			label := "  " + b.label + "  "
			width := len([]rune(label))
			if col+width > ws.cols {
				break
			}
			style := "\x1b[7m"
			if (b.key == keyZoomOut || b.key == keyZoomReset) && zm.level <= 1 {
				style = "\x1b[2;7m"
			}
			text(lay.footerRow, col, style, label, width)
			b.col0, b.col1 = col, col+width-1
			buttons = append(buttons, b)
			col += width + 1
		}
		status := ""
		if zm.level > 1 {
			status = fmt.Sprintf("%g×", zm.level)
		}
		if cur != nil {
			_, n, done := cur.frame(0)
			total := clock(float64(n) / cur.fps)
			if !done {
				total += "…"
			}
			status = strings.TrimSpace(fmt.Sprintf("%s / %s  %s", clock(float64(frameIdx)/cur.fps), total, status))
		}
		hint := "tap image to zoom · ← → + − 0 · space · q"
		if zm.level > 1 {
			hint = "tap to center · arrows pan · 0 fit · q"
		}
		if room := ws.cols - col - 1; room > 0 {
			line := status
			if line != "" && room > len([]rune(line))+len([]rune(hint))+3 {
				line += "  ·  " + hint
			} else if line == "" {
				line = hint
			}
			text(lay.footerRow, col+1, "\x1b[2m", line, room)
		}
	}

	drawFrame := func() {
		if cur == nil {
			return
		}
		img, n, done := cur.frame(frameIdx)
		if n == 0 {
			return
		}
		if frameIdx >= n {
			if done {
				frameIdx = 0
			} else {
				frameIdx = n - 1
			}
			playFrom, playBase = time.Now(), frameIdx
			img, _, _ = cur.frame(frameIdx)
		}
		if frameIdx == shownFrame {
			return
		}
		next := idFrameA + idFrameB - frameID
		kittyUpload(w, img, next)
		b, src := zm.view(lay.main, img.w, img.h, ws)
		kittyPlaceRect(w, next, b, src)
		kittyDelete(w, frameID)
		frameID = next
		shownFrame = frameIdx
		drawFooter()
	}

	stripStart := func() int {
		count := len(lay.thumbs)
		return max(0, min(idx-count/2, len(entries)-count))
	}

	drawStrip := func() {
		if len(lay.thumbs) == 0 || len(entries) == 0 {
			return
		}
		cw, ch := ws.cell()
		start := stripStart()
		for slot, b := range lay.thumbs {
			i := start + slot
			id := idThumb0 + slot
			kittyDelete(w, id)
			fmt.Fprintf(w, "\x1b[%d;%dH%s", lay.markerRow, b.col, strings.Repeat(" ", b.cols))
			if i >= len(entries) {
				continue
			}
			e := entries[i]
			maxW, maxH := int(float64(b.cols)*cw), int(float64(b.rows)*ch)
			k := thumbKey(e.Path, maxW, maxH)
			if img, ok := thumbs[k]; ok && img.w > 0 {
				kittyUpload(w, img, id)
				kittyPlace(w, id, place(b, img.w, img.h, ws))
			} else if !ok && !pending[k] {
				pending[k] = true
				jobs <- thumbJob{e.Path, e.Kind, maxW, maxH}
			}
			switch {
			case i == idx:
				fmt.Fprintf(w, "\x1b[%d;%dH\x1b[34m%s\x1b[0m", lay.markerRow, b.col, strings.Repeat("━", b.cols))
			case e.Kind == kindVideo:
				fmt.Fprintf(w, "\x1b[%d;%dH\x1b[2m▶ video\x1b[0m", lay.markerRow, b.col)
			}
		}
	}

	draw := func() {
		fmt.Fprint(w, "\x1b_Ga=d,d=A,q=2\x1b\\\x1b[H\x1b[2J")
		shownFrame = -1
		if idx < 0 {
			msg := "Waiting for screenshots and recordings…"
			text(ws.rows/2, max(1, (ws.cols-len([]rune(msg)))/2), "\x1b[2m", msg, ws.cols)
			w.Flush()
			return
		}
		e := entries[idx]
		count := fmt.Sprintf("%d/%d  ", idx+1, len(entries))
		text(1, 2, "\x1b[2m", count, ws.cols-2)
		text(1, 2+len(count), "\x1b[1m", filepath.Base(e.Path), ws.cols-2-len(count))
		if e.Caption != "" {
			text(2, 2, "\x1b[2m", e.Caption, ws.cols-2)
		}
		switch {
		case stillErr != nil:
			text(lay.main.row+lay.main.rows/2, 2, "\x1b[31m", stillErr.Error(), ws.cols-2)
		case cur != nil:
			if _, n, done := cur.frame(0); n == 0 {
				msg := "Decoding…"
				if done && cur.err != nil {
					msg = cur.err.Error()
				}
				text(lay.main.row+lay.main.rows/2, 2, "\x1b[2m", msg, ws.cols-2)
			}
			drawFrame()
		case still.w > 0:
			kittyUpload(w, still, idStill)
			b, src := zm.view(lay.main, still.w, still.h, ws)
			kittyPlaceRect(w, idStill, b, src)
		}
		drawStrip()
		drawFooter()
		w.Flush()
	}

	reload()
	load()
	draw()

	tick := time.NewTicker(time.Second / clipFPS)
	defer tick.Stop()
	for {
		select {
		case <-quit:
			stopClip()
			return nil
		case <-winch:
			ws = getWinSize(in)
			lay = makeLayout(ws)
			stillPath = ""
			stopClip()
			load()
			draw()
		case r := <-results:
			delete(pending, r.key)
			thumbs[r.key] = r.img
			drawStrip()
			w.Flush()
		case <-wake:
			if cur != nil {
				if _, n, done := cur.frame(0); n <= 1 || done {
					draw()
				} else {
					drawFooter()
					w.Flush()
				}
			}
		case in, open := <-keys:
			if !open || in.key == keyQuit {
				stopClip()
				return nil
			}
			// mediaSize is the size of what the main view shows, in pixels.
			mw, mh := 0, 0
			switch {
			case cur != nil:
				mw, mh = cur.w, cur.h
			case still.w > 0 && stillPath != "":
				mw, mh = still.w, still.h
			}
			zoomed := zm.level > 1 && mw > 0
			before, beforeZoom := idx, zm
			pan := func(dx, dy float64) {
				_, r := zm.view(lay.main, mw, mh, ws)
				zm.cx += dx * 0.2 * float64(r.w) / float64(mw)
				zm.cy += dy * 0.2 * float64(r.h) / float64(mh)
				zm = zm.clamped(lay.main, mw, mh, ws)
			}
			k := in.key
			if k == keyTap {
				k = keyNone
				switch {
				case in.row == lay.footerRow:
					for _, b := range buttons {
						if in.col >= b.col0 && in.col <= b.col1 {
							k = b.key
						}
					}
					// Buttons zoom around the middle, not the bottom row.
					in.row, in.col = 0, 0
				case len(lay.thumbs) > 0 && in.row >= lay.thumbs[0].row && in.row <= lay.markerRow:
					for slot, b := range lay.thumbs {
						if in.col >= b.col && in.col < b.col+b.cols && stripStart()+slot < len(entries) {
							idx = stripStart() + slot
						}
					}
				case mw > 0:
					shown, r := zm.view(lay.main, mw, mh, ws)
					if in.row < shown.row || in.row >= shown.row+shown.rows || in.col < shown.col || in.col >= shown.col+shown.cols {
						break
					}
					if zm.level <= 1 {
						k = keyZoomToggle
						break
					}
					fx := (float64(in.col-shown.col) + 0.5) / float64(shown.cols)
					fy := (float64(in.row-shown.row) + 0.5) / float64(shown.rows)
					zm.cx = (float64(r.x) + fx*float64(r.w)) / float64(mw)
					zm.cy = (float64(r.y) + fy*float64(r.h)) / float64(mh)
					zm = zm.clamped(lay.main, mw, mh, ws)
				}
			}
			if !zoomed {
				switch k {
				case keyLeft, keyUp:
					k = keyPrev
				case keyRight, keyDown:
					k = keyNext
				}
			}
			switch k {
			case keyPrev:
				idx = max(0, idx-1)
			case keyNext:
				idx = min(len(entries)-1, idx+1)
			case keyFirst:
				idx = min(0, len(entries)-1)
			case keyLast:
				idx = len(entries) - 1
			case keyLeft:
				pan(-1, 0)
			case keyRight:
				pan(1, 0)
			case keyUp:
				pan(0, -1)
			case keyDown:
				pan(0, 1)
			case keyPlay:
				playing = !playing
				playFrom, playBase = time.Now(), frameIdx
			case keyStepBack:
				playing = false
				frameIdx = max(0, frameIdx-1)
			case keyStep:
				playing = false
				frameIdx++
			case keyZoomIn, keyZoomOut, keyZoomToggle, keyZoomReset, keyWheelIn, keyWheelOut:
				if mw == 0 {
					break
				}
				level := 1.0
				switch k {
				case keyZoomIn, keyWheelIn:
					level = zm.step(1)
				case keyZoomOut, keyWheelOut:
					level = zm.step(-1)
				case keyZoomToggle:
					if zm.level <= 1 {
						level = 2
					}
				}
				row, col := in.row, in.col
				if row == 0 {
					row, col = lay.main.row+lay.main.rows/2, lay.main.col+lay.main.cols/2
				}
				zm = zm.zoomAt(level, row, col, lay.main, mw, mh, ws)
				if level <= 1 {
					zm = zoom{1, 0.5, 0.5}
				}
			}
			follow = idx == len(entries)-1
			switch {
			case idx != before:
				zm = zoom{1, 0.5, 0.5}
				load()
				draw()
			case zm != beforeZoom && cur != nil:
				shownFrame = -1
				drawFrame()
				drawFooter()
				w.Flush()
			case zm != beforeZoom:
				b, src := zm.view(lay.main, mw, mh, ws)
				kittyPlaceRect(w, idStill, b, src)
				drawFooter()
				w.Flush()
			default:
				drawFrame()
				drawFooter()
				w.Flush()
			}
		case now := <-tick.C:
			if now.Sub(lastScan) > 700*time.Millisecond && reload() {
				if idx < 0 || (entries[idx].Path != stillPath && (cur == nil || cur.path != entries[idx].Path)) {
					zm = zoom{1, 0.5, 0.5}
				}
				load()
				draw()
				continue
			}
			if cur != nil && playing {
				frameIdx = playBase + int(now.Sub(playFrom).Seconds()*cur.fps)
				drawFrame()
				w.Flush()
			}
		}
	}
}
