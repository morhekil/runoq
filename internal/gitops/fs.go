package gitops

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveGitDir returns the path to the actual .git directory.
// For regular repos this is root/.git. For worktrees it reads the
// gitdir pointer from the .git file.
func resolveGitDir(root string) (string, error) {
	gitPath := filepath.Join(root, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		return "", fmt.Errorf("no .git at %s: %w", root, err)
	}
	if info.IsDir() {
		return gitPath, nil
	}
	// .git is a file — read the gitdir pointer
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(data))
	if after, ok := strings.CutPrefix(line, "gitdir: "); ok {
		if filepath.IsAbs(after) {
			return after, nil
		}
		return filepath.Join(root, after), nil
	}
	return "", fmt.Errorf("unexpected .git file content: %s", line)
}

// fsBranchExists checks if a branch exists by looking at refs/heads and packed-refs.
func fsBranchExists(gitDir string, branch string) (bool, error) {
	// Check loose ref
	refPath := filepath.Join(gitDir, "refs", "heads", branch)
	if _, err := os.Stat(refPath); err == nil {
		return true, nil
	}
	// Check packed-refs
	return packedRefExists(gitDir, "refs/heads/"+branch)
}

// fsDeleteBranch removes a local branch by deleting its ref file and packed-refs entry.
func fsDeleteBranch(gitDir string, branch string) error {
	ref := "refs/heads/" + branch
	refPath := filepath.Join(gitDir, ref)
	if err := os.Remove(refPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	// Also remove from packed-refs if present
	return removePackedRef(gitDir, ref)
}

// fsWorktreePrune removes stale worktree admin entries where the linked directory no longer exists.
func fsWorktreePrune(gitDir string) error {
	worktreesDir := filepath.Join(gitDir, "worktrees")
	entries, err := os.ReadDir(worktreesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no worktrees
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		gitdirFile := filepath.Join(worktreesDir, entry.Name(), "gitdir")
		data, err := os.ReadFile(gitdirFile)
		if err != nil {
			continue
		}
		linkedDir := strings.TrimSpace(string(data))
		if _, err := os.Stat(linkedDir); os.IsNotExist(err) {
			_ = os.RemoveAll(filepath.Join(worktreesDir, entry.Name()))
		}
	}
	return nil
}

func packedRefExists(gitDir string, ref string) (bool, error) {
	f, err := os.Open(filepath.Join(gitDir, "packed-refs"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer func() {
		_ = f.Close()
	}()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		if _, after, ok := strings.Cut(line, " "); ok && after == ref {
			return true, nil
		}
	}
	return false, scanner.Err()
}

func removePackedRef(gitDir string, ref string) error {
	packedPath := filepath.Join(gitDir, "packed-refs")
	f, err := os.Open(packedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if _, after, ok := strings.Cut(line, " "); ok && after == ref {
			continue // skip this ref
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return os.WriteFile(packedPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
