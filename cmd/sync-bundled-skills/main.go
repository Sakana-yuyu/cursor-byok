// sync-bundled-skills copies agent skills from .agents/skills into internal/skills/bundled
// when both trees share a skill name. Bundled-only skills are left untouched.
package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	agentsSkillsRel  = ".agents/skills"
	bundledSkillsRel = "internal/skills/bundled"
)

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repository root containing go.mod was not found from %s", dir)
		}
		dir = parent
	}
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func syncSkill(sourceDir, targetDir string, dryRun bool) (int, error) {
	written := 0
	err := filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(targetDir, rel)
		if dryRun {
			srcData, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			dstData, err := os.ReadFile(dst)
			if err != nil {
				return fmt.Errorf("missing bundled file %s (run without --check)", rel)
			}
			if string(srcData) != string(dstData) {
				return fmt.Errorf("bundled skill drift: %s", rel)
			}
			return nil
		}
		if err := copyFile(path, dst); err != nil {
			return err
		}
		written++
		return nil
	})
	return written, err
}

func main() {
	check := flag.Bool("check", false, "verify bundled skills match .agents/skills without writing")
	flag.Parse()

	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	agentsRoot := filepath.Join(root, agentsSkillsRel)
	bundledRoot := filepath.Join(root, bundledSkillsRel)
	bundledEntries, err := os.ReadDir(bundledRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	totalWritten := 0
	for _, entry := range bundledEntries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		sourceDir := filepath.Join(agentsRoot, name)
		if _, err := os.Stat(sourceDir); err != nil {
			continue
		}
		targetDir := filepath.Join(bundledRoot, name)
		written, err := syncSkill(sourceDir, targetDir, *check)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sync %s: %v\n", name, err)
			os.Exit(1)
		}
		totalWritten += written
	}

	if *check {
		fmt.Println("bundled skills are in sync with .agents/skills")
		return
	}
	fmt.Printf("synced %d files into %s\n", totalWritten, bundledSkillsRel)
}
