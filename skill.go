package main

import (
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed skills/rexshow/SKILL.md
var skillMD []byte

func runSkill(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: rexshow skill show | install [--dir SKILLS_DIR] | path [--dir SKILLS_DIR]")
	}
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("skill "+args[0], flag.ContinueOnError)
	dir := fs.String("dir", filepath.Join(home, ".claude", "skills"), "skills directory")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	target := filepath.Join(*dir, "rexshow", "SKILL.md")
	switch args[0] {
	case "show":
		_, err := os.Stdout.Write(skillMD)
		return err
	case "path":
		fmt.Println(target)
		return nil
	case "install":
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, skillMD, 0o644); err != nil {
			return err
		}
		fmt.Println("installed the rexshow skill at", target)
		return nil
	}
	return fmt.Errorf("unknown skill command %q", args[0])
}
