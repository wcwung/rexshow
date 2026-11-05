package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const usage = `rexshow: show screenshots and screen recordings in a Rex pane (kitty graphics)

Usage: rexshow [--list NAME] COMMAND [ARGS]

  add [-c CAPTION] FILE...   add images or videos; the viewer jumps to the newest
  watch DIR...               also show every image and video that lands in DIR
  unwatch DIR...             stop watching DIR
  show [--below] [--ratio PCT]
                             open the viewer beside your pane (40% by default, no focus change)
  hide                       close the viewer pane
  list                       print what the viewer shows, oldest first
  clear                      forget all files and folders
  view                       run the viewer in this terminal
  skill show | install [--dir DIR] | path

Images: png, jpg, gif, webp, bmp. Videos (needs ffmpeg): mp4, webm, mov, mkv.
Each Rex pane gets its own show ($REX_BLOCK); --list or $REXSHOW_LIST shares one.
In the viewer: ← → item, g first, G newest, space play/pause, , . step a frame, q close.
Zoom: + − or the mouse wheel, z for 2×, 0 to fit; while zoomed, arrows or h j k l pan and n p change item.
Touch and mouse (Rex on iOS sends taps only): the buttons at the bottom, tap a thumbnail
to open it, tap the image to zoom to 2× there, and tap again to move that point to the middle.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "rexshow:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	global := flag.NewFlagSet("rexshow", flag.ContinueOnError)
	global.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	listFlag := global.String("list", "", "show name")
	if err := global.Parse(args); err != nil {
		return err
	}
	args = global.Args()
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	name := showName(*listFlag)
	switch cmd {
	case "add":
		fs := flag.NewFlagSet("add", flag.ContinueOnError)
		caption := fs.String("c", "", "caption")
		fs.StringVar(caption, "caption", "", "caption")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			return errors.New("usage: rexshow add [-c CAPTION] FILE...")
		}
		return update(name, func(s *Show) error {
			for _, f := range fs.Args() {
				path, err := absFile(f)
				if err != nil {
					return err
				}
				if kindOf(path) == kindNone {
					return fmt.Errorf("%s: not an image or video this viewer reads", f)
				}
				s.add(path, *caption)
			}
			return nil
		})
	case "watch", "unwatch":
		if len(rest) == 0 {
			return fmt.Errorf("usage: rexshow %s DIR...", cmd)
		}
		return update(name, func(s *Show) error {
			for _, d := range rest {
				dir, err := filepath.Abs(d)
				if err != nil {
					return err
				}
				if cmd == "watch" {
					if info, err := os.Stat(dir); err != nil || !info.IsDir() {
						return fmt.Errorf("%s: not a folder", d)
					}
					s.watch(dir)
					continue
				}
				kept := s.Dirs[:0]
				for _, x := range s.Dirs {
					if x != dir {
						kept = append(kept, x)
					}
				}
				s.Dirs = kept
			}
			return nil
		})
	case "show":
		fs := flag.NewFlagSet("show", flag.ContinueOnError)
		below := fs.Bool("below", false, "split downward")
		ratio := fs.Int("ratio", 40, "the viewer's share of the split, in percent")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		split := "right"
		if *below {
			split = "below"
		}
		return showPane(name, showOptions{split: split, ratio: *ratio})
	case "hide":
		return hidePane(name)
	case "list":
		s, err := loadShow(name)
		if err != nil {
			return err
		}
		for _, d := range s.Dirs {
			fmt.Println("watching", d)
		}
		for _, e := range s.entries() {
			line := e.Path
			if e.Caption != "" {
				line += "  # " + e.Caption
			}
			fmt.Println(line)
		}
		return nil
	case "clear":
		return saveShow(name, &Show{})
	case "view":
		return runView(name)
	case "skill":
		return runSkill(rest)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	}
	return fmt.Errorf("unknown command %q (try rexshow help)", cmd)
}

func update(name string, change func(*Show) error) error {
	s, err := loadShow(name)
	if err != nil {
		return err
	}
	if err := change(s); err != nil {
		return err
	}
	return saveShow(name, s)
}

func absFile(f string) (string, error) {
	path, err := filepath.Abs(strings.TrimSpace(f))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a folder; use rexshow watch", f)
	}
	return path, nil
}
