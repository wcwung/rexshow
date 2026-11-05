package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

type Item struct {
	Path    string    `json:"path"`
	Caption string    `json:"caption,omitempty"`
	Added   time.Time `json:"added"`
}

// Show is one viewer's state: files added one by one, plus folders whose
// media files all appear in it.
type Show struct {
	Items []Item   `json:"items"`
	Dirs  []string `json:"dirs,omitempty"`
}

type mediaKind int

const (
	kindNone mediaKind = iota
	kindImage
	kindVideo
)

func kindOf(path string) mediaKind {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return kindImage
	case ".mp4", ".webm", ".mov", ".mkv", ".m4v":
		return kindVideo
	}
	return kindNone
}

func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "rexshow")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "rexshow")
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// showName picks the show: --list, then $REXSHOW_LIST, then the agent's pane
// ($REX_BLOCK) so each agent gets its own, then "default".
func showName(flag string) string {
	name := flag
	if name == "" {
		name = os.Getenv("REXSHOW_LIST")
	}
	if name == "" {
		name = strings.TrimPrefix(os.Getenv("REX_BLOCK"), "block:")
	}
	if name == "" {
		name = "default"
	}
	return unsafeName.ReplaceAllString(name, "_")
}

func showPath(name string) string { return filepath.Join(stateDir(), name+".json") }

func loadShow(name string) (*Show, error) {
	data, err := os.ReadFile(showPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return &Show{}, nil
	}
	if err != nil {
		return nil, err
	}
	var s Show
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveShow(name string, s *Show) error {
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := showPath(name) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, showPath(name))
}

// Entry is one thing the viewer can show, in the order it shows them.
type Entry struct {
	Path    string
	Caption string
	When    time.Time
	Kind    mediaKind
}

// entries merges the added files and the watched folders' media, oldest
// first, so the newest is always last.
func (s *Show) entries() []Entry {
	seen := map[string]bool{}
	var out []Entry
	for _, it := range s.Items {
		if seen[it.Path] {
			continue
		}
		if _, err := os.Stat(it.Path); err != nil {
			continue
		}
		seen[it.Path] = true
		out = append(out, Entry{it.Path, it.Caption, it.Added, kindOf(it.Path)})
	}
	for _, dir := range s.Dirs {
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			path := filepath.Join(dir, f.Name())
			if f.IsDir() || seen[path] || kindOf(path) == kindNone || strings.HasPrefix(f.Name(), ".") {
				continue
			}
			info, err := f.Info()
			// A file still being written (a recording until it stops) is left
			// out until it has been quiet for a moment.
			settle := time.Second
			if kindOf(path) == kindVideo {
				settle = 3 * time.Second
			}
			if err != nil || info.Size() == 0 || time.Since(info.ModTime()) < settle {
				continue
			}
			seen[path] = true
			out = append(out, Entry{path, "", info.ModTime(), kindOf(path)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].When.Before(out[j].When) })
	return out
}

func (s *Show) add(path, caption string) {
	s.Items = slices.DeleteFunc(s.Items, func(it Item) bool { return it.Path == path })
	s.Items = append(s.Items, Item{Path: path, Caption: caption, Added: time.Now()})
}

func (s *Show) watch(dir string) {
	if !slices.Contains(s.Dirs, dir) {
		s.Dirs = append(s.Dirs, dir)
	}
}
