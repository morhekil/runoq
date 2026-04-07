package gitops

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/saruman/runoq/internal/shell"
)

// cliRepo implements Repo by shelling out to the git CLI.
type cliRepo struct {
	root string
	ctx  context.Context
	exec shell.CommandExecutor
}

// OpenCLI creates a Repo backed by git CLI commands.
func OpenCLI(ctx context.Context, root string, exec shell.CommandExecutor) Repo {
	return &cliRepo{root: root, ctx: ctx, exec: exec}
}

func (r *cliRepo) Root() string { return r.root }

func (r *cliRepo) git(args ...string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := r.exec(r.ctx, shell.CommandRequest{
		Name:   "git",
		Args:   append([]string{"-C", r.root}, args...),
		Dir:    r.root,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (r *cliRepo) gitQuiet(args ...string) error {
	return r.exec(r.ctx, shell.CommandRequest{
		Name:   "git",
		Args:   append([]string{"-C", r.root}, args...),
		Dir:    r.root,
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
}

func (r *cliRepo) ResolveHEAD() (string, error) {
	return r.git("log", "-1", "--format=%H")
}

func (r *cliRepo) CommitExists(sha string) (bool, error) {
	err := r.gitQuiet("rev-parse", "--verify", sha+"^{commit}")
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (r *cliRepo) BranchExists(branch string) (bool, error) {
	err := r.gitQuiet("rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (r *cliRepo) CommitLog(base, head string) ([]Commit, error) {
	out, err := r.git("log", "--reverse", "--format=%H %s", base+".."+head)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var commits []Commit
	for line := range strings.SplitSeq(out, "\n") {
		sha, subject, _ := strings.Cut(line, " ")
		commits = append(commits, Commit{SHA: strings.TrimSpace(sha), Subject: strings.TrimSpace(subject)})
	}
	return commits, nil
}

func (r *cliRepo) DiffNameStatus(base, head string) ([]FileChange, error) {
	out, err := r.git("diff", "--name-status", base+".."+head)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var changes []FileChange
	for line := range strings.SplitSeq(out, "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			changes = append(changes, FileChange{Status: parts[0], Path: parts[1]})
		}
	}
	return changes, nil
}

func (r *cliRepo) DiffTreeFiles(commitSHA string) ([]string, error) {
	out, err := r.git("diff-tree", "--no-commit-id", "--name-only", "-r", commitSHA)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var files []string
	for line := range strings.SplitSeq(out, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files, nil
}

func (r *cliRepo) FileChanged(sha1, sha2, path string) (bool, error) {
	err := r.gitQuiet("diff", "--quiet", sha1, sha2, "--", path)
	if err != nil {
		return true, nil // non-zero exit = file differs
	}
	return false, nil
}

func (r *cliRepo) RemoteURL(name string) (string, error) {
	return r.git("remote", "get-url", name)
}

func (r *cliRepo) RemoteRefExists(remote, branch string) (string, bool, error) {
	out, err := r.git("ls-remote", "--heads", remote, branch)
	if err != nil {
		return "", false, err
	}
	if out == "" {
		return "", false, nil
	}
	sha, _, _ := strings.Cut(out, "\t")
	return strings.TrimSpace(sha), true, nil
}

func (r *cliRepo) DefaultBranch(remote string) (string, error) {
	out, err := r.git("ls-remote", "--symref", remote, "HEAD")
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "ref: ") {
			// "ref: refs/heads/main\tHEAD"
			ref, _, _ := strings.Cut(strings.TrimPrefix(line, "ref: "), "\t")
			return strings.TrimPrefix(ref, "refs/heads/"), nil
		}
	}
	return "", fmt.Errorf("could not determine default branch from %s", remote)
}

func (r *cliRepo) Fetch(remote string, refs ...string) error {
	args := append([]string{"fetch", remote}, refs...)
	return r.gitQuiet(args...)
}

func (r *cliRepo) MergeBase(sha1, sha2 string) (string, error) {
	return r.git("merge-base", sha1, sha2)
}

func (r *cliRepo) MergeHasConflicts(base, ours, theirs string) (bool, error) {
	out, err := r.git("merge-tree", base, ours, theirs)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "<<<<<<<"), nil
}

func (r *cliRepo) WorktreeAdd(path, branch, base string) error {
	return r.gitQuiet("worktree", "add", path, "-b", branch, base)
}

func (r *cliRepo) WorktreeRemove(path string) error {
	return r.gitQuiet("worktree", "remove", path, "--force")
}

func (r *cliRepo) WorktreePrune() error {
	return r.gitQuiet("worktree", "prune")
}

func (r *cliRepo) DeleteBranch(branch string) error {
	return r.gitQuiet("branch", "-D", branch)
}

func (r *cliRepo) SetConfig(key, value string) error {
	return r.gitQuiet("config", key, value)
}

func (r *cliRepo) CommitEmpty(dir, message string) error {
	var stderr bytes.Buffer
	err := r.exec(r.ctx, shell.CommandRequest{
		Name:   "git",
		Args:   []string{"-C", dir, "commit", "--allow-empty", "-m", message},
		Dir:    dir,
		Stdout: io.Discard,
		Stderr: &stderr,
	})
	if err != nil {
		return fmt.Errorf("git commit: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (r *cliRepo) Push(dir, remote, branch string) error {
	var stderr bytes.Buffer
	err := r.exec(r.ctx, shell.CommandRequest{
		Name:   "git",
		Args:   []string{"-C", dir, "push", "-u", remote, branch},
		Dir:    dir,
		Stdout: io.Discard,
		Stderr: &stderr,
	})
	if err != nil {
		return fmt.Errorf("git push: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
