// Package gitops provides a clean interface for all git operations used by runoq.
// Callers interact with the Repo interface; implementations can be CLI-backed,
// go-git-backed, or filesystem-backed depending on the operation.
package gitops

import "io"

// Repo abstracts all git operations needed by runoq.
type Repo interface {
	// Root returns the repository root path.
	Root() string

	// ResolveHEAD returns the SHA of the current HEAD commit.
	ResolveHEAD() (string, error)

	// CommitExists checks whether a commit SHA exists in the repository.
	CommitExists(sha string) (bool, error)

	// BranchExists checks whether a local branch exists.
	BranchExists(branch string) (bool, error)

	// CommitLog returns commits from base..head in oldest-first order.
	CommitLog(base, head string) ([]Commit, error)

	// DiffNameStatus returns files changed between two commits with their status.
	DiffNameStatus(base, head string) ([]FileChange, error)

	// DiffTreeFiles returns file paths touched by a single commit.
	DiffTreeFiles(commitSHA string) ([]string, error)

	// FileChanged reports whether a file differs between two commits.
	FileChanged(sha1, sha2, path string) (bool, error)

	// RemoteURL returns the URL of a named remote.
	RemoteURL(name string) (string, error)

	// RemoteRefExists checks if a branch exists on a remote, returning its SHA.
	RemoteRefExists(remote, branch string) (string, bool, error)

	// DefaultBranch returns the default branch name for a remote.
	DefaultBranch(remote string) (string, error)

	// Fetch fetches refs from a remote.
	Fetch(remote string, refs ...string) error

	// MergeBase finds the common ancestor of two commits.
	MergeBase(sha1, sha2 string) (string, error)

	// MergeHasConflicts simulates a three-way merge and reports whether conflicts exist.
	MergeHasConflicts(base, ours, theirs string) (bool, error)

	// WorktreeAdd creates a new worktree at path with the given branch based on base.
	WorktreeAdd(path, branch, base string) error

	// WorktreeRemove removes a worktree at the given path.
	WorktreeRemove(path string) error

	// WorktreePrune removes stale worktree metadata.
	WorktreePrune() error

	// DeleteBranch deletes a local branch.
	DeleteBranch(branch string) error

	// SetConfig sets a git config value in the repository.
	SetConfig(key, value string) error

	// CommitEmpty creates an empty commit with the given message in the specified directory.
	CommitEmpty(dir, message string) error

	// Push pushes a branch to a remote from the specified directory.
	Push(dir, remote, branch string) error
}

// Commit represents a single commit in the log.
type Commit struct {
	SHA     string
	Subject string
}

// FileChange represents a file change between two commits.
type FileChange struct {
	Status string // "A", "M", "D", "R"
	Path   string
}

// SetLogWriter sets a writer for capturing all git subprocess output.
// Implementations that shell out to git should tee stdout/stderr to this writer.
func SetLogWriter(r Repo, w io.Writer) {
	type logWritable interface {
		SetLogWriter(io.Writer)
	}
	if lw, ok := r.(logWritable); ok {
		lw.SetLogWriter(w)
	}
}
