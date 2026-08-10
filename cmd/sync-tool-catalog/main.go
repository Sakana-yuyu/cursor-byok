// sync-tool-catalog renders the reviewed Cursor protocol capability map.
// It intentionally does not synthesize tool schemas from <Name>Args names:
// those names also describe controls and shared protocol structures.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"cursor/internal/backend/forwarder"
)

const capabilityMapRelPath = "docs/cursor-capability-map.md"

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

func main() {
	write := flag.Bool("write", false, "write docs/cursor-capability-map.md")
	flag.Parse()

	contents := forwarder.RenderCursorCapabilityMap()
	if !*write {
		fmt.Print(contents)
		return
	}
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	path := filepath.Join(root, capabilityMapRelPath)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", capabilityMapRelPath, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "wrote %s\n", capabilityMapRelPath)
}
