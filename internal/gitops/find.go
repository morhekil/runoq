package gitops

import (
	"fmt"
	"os"
	"path/filepath"
)

// FindRoot walks up from startDir looking for a .git directory or file.
// Returns the directory containing .git, or an error if none is found.
func FindRoot(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not a git repository (or any parent up to /): %s", startDir)
		}
		dir = parent
	}
}
