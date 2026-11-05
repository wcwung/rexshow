package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// The viewer pane's block ID is kept beside the show so `show` is idempotent
// and `hide` can close it.
func viewerPath(name string) string { return filepath.Join(stateDir(), name+".viewer") }

func viewerOpen(name string) (string, bool) {
	data, err := os.ReadFile(viewerPath(name))
	if err != nil {
		return "", false
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", false
	}
	if err := exec.Command("rex", "block", "inspect", id).Run(); err != nil {
		return id, false
	}
	return id, true
}

type showOptions struct {
	split string
	ratio int
}

func inRex() bool {
	_, err := exec.LookPath("rex")
	return err == nil && (os.Getenv("REX_BLOCK") != "" || os.Getenv("REX_SESSION") != "")
}

func showPane(name string, opt showOptions) error {
	if id, ok := viewerOpen(name); ok {
		fmt.Println(id)
		return nil
	}
	if !inRex() {
		return errors.New("not running inside Rex; run `rexshow view` in a terminal that supports the kitty graphics protocol")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"split", "--focus=false", "--split", opt.split, "--ratio", strconv.Itoa(opt.ratio),
		"--shell=none", "--label", "Media"}
	if b := os.Getenv("REX_BLOCK"); b != "" {
		args = append(args, "-b", b)
	}
	args = append(args, "--", self, "--list", name, "view")
	cmd := exec.Command("rex", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("rex split: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return errors.New("rex split printed no block ID")
	}
	id := fields[len(fields)-1]
	// rex split lands at 50/50 whatever --ratio says; a move applies the ratio.
	if b := os.Getenv("REX_BLOCK"); b != "" {
		_ = exec.Command("rex", "block", "move", id, opt.split, b, "--ratio", strconv.Itoa(opt.ratio)).Run()
	}
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(viewerPath(name), []byte(id+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

func hidePane(name string) error {
	id, ok := viewerOpen(name)
	os.Remove(viewerPath(name))
	if !ok {
		return nil
	}
	return exec.Command("rex", "block", "close", id).Run()
}
