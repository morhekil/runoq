package gitops

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/utils/merkletrie"
	"github.com/saruman/runoq/internal/shell"
)

// cliRepo implements Repo using go-git where possible, with CLI fallbacks
// for environments where PlainOpen is unavailable (e.g. mock-based tests).
type cliRepo struct {
	root string
	ctx  context.Context
	exec shell.CommandExecutor
}

// OpenCLI creates a Repo backed by go-git with CLI fallbacks.
func OpenCLI(ctx context.Context, root string, exec shell.CommandExecutor) Repo {
	return &cliRepo{root: root, ctx: ctx, exec: exec}
}

func (r *cliRepo) Root() string { return r.root }

func (r *cliRepo) open(root string) (*git.Repository, error) {
	return git.PlainOpen(root)
}

// --- CLI helpers (used by fallbacks and B6 methods) ---

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

func (r *cliRepo) gitDir(dir string, args ...string) error {
	var stderr bytes.Buffer
	err := r.exec(r.ctx, shell.CommandRequest{
		Name:   "git",
		Args:   append([]string{"-C", dir}, args...),
		Dir:    dir,
		Stdout: io.Discard,
		Stderr: &stderr,
	})
	if err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// --- B4: Read operations (go-git with CLI fallback) ---

func (r *cliRepo) ResolveHEAD() (string, error) {
	repo, err := r.open(r.root)
	if err != nil {
		return r.git("log", "-1", "--format=%H")
	}
	ref, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("ResolveHEAD: %w", err)
	}
	return ref.Hash().String(), nil
}

func (r *cliRepo) CommitExists(sha string) (bool, error) {
	repo, err := r.open(r.root)
	if err != nil {
		err := r.gitQuiet("rev-parse", "--verify", sha+"^{commit}")
		if err != nil {
			return false, nil
		}
		return true, nil
	}
	h, err := resolveHash(repo, sha)
	if err != nil {
		return false, nil
	}
	_, err = repo.CommitObject(h)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (r *cliRepo) BranchExists(branch string) (bool, error) {
	gitDir, err := resolveGitDir(r.root)
	if err != nil {
		return false, err
	}
	return fsBranchExists(gitDir, branch)
}

func (r *cliRepo) CommitLog(base, head string) ([]Commit, error) {
	repo, err := r.open(r.root)
	if err != nil {
		return r.commitLogCLI(base, head)
	}
	headHash, err := resolveHash(repo, head)
	if err != nil {
		return nil, fmt.Errorf("CommitLog: %w", err)
	}
	baseHash, err := resolveHash(repo, base)
	if err != nil {
		return nil, fmt.Errorf("CommitLog: %w", err)
	}

	iter, err := repo.Log(&git.LogOptions{From: headHash})
	if err != nil {
		return nil, fmt.Errorf("CommitLog: %w", err)
	}
	defer iter.Close()

	var commits []Commit
	err = iter.ForEach(func(c *object.Commit) error {
		if c.Hash.Equal(baseHash) {
			return errStop
		}
		subject := strings.SplitN(c.Message, "\n", 2)[0]
		commits = append(commits, Commit{SHA: c.Hash.String(), Subject: strings.TrimSpace(subject)})
		return nil
	})
	if err != nil && err != errStop {
		return nil, fmt.Errorf("CommitLog: %w", err)
	}

	// Reverse to oldest-first order
	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}
	return commits, nil
}

func (r *cliRepo) commitLogCLI(base, head string) ([]Commit, error) {
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
	repo, err := r.open(r.root)
	if err != nil {
		return r.diffNameStatusCLI(base, head)
	}
	baseTree, err := r.commitTree(repo, base)
	if err != nil {
		return nil, fmt.Errorf("DiffNameStatus base: %w", err)
	}
	headTree, err := r.commitTree(repo, head)
	if err != nil {
		return nil, fmt.Errorf("DiffNameStatus head: %w", err)
	}

	diffs, err := object.DiffTree(baseTree, headTree)
	if err != nil {
		return nil, fmt.Errorf("DiffNameStatus: %w", err)
	}

	var changes []FileChange
	for _, ch := range diffs {
		action, err := ch.Action()
		if err != nil {
			return nil, err
		}
		var status string
		var path string
		switch action {
		case merkletrie.Insert:
			status = "A"
			path = ch.To.Name
		case merkletrie.Delete:
			status = "D"
			path = ch.From.Name
		case merkletrie.Modify:
			status = "M"
			path = ch.To.Name
		}
		changes = append(changes, FileChange{Status: status, Path: path})
	}
	return changes, nil
}

func (r *cliRepo) diffNameStatusCLI(base, head string) ([]FileChange, error) {
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
	repo, err := r.open(r.root)
	if err != nil {
		return r.diffTreeFilesCLI(commitSHA)
	}
	h, err := resolveHash(repo, commitSHA)
	if err != nil {
		return nil, fmt.Errorf("DiffTreeFiles: %w", err)
	}
	commit, err := repo.CommitObject(h)
	if err != nil {
		return nil, fmt.Errorf("DiffTreeFiles: %w", err)
	}

	commitTree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("DiffTreeFiles: %w", err)
	}

	var parentTree *object.Tree
	if len(commit.ParentHashes) > 0 {
		parent, err := repo.CommitObject(commit.ParentHashes[0])
		if err != nil {
			return nil, fmt.Errorf("DiffTreeFiles parent: %w", err)
		}
		parentTree, err = parent.Tree()
		if err != nil {
			return nil, fmt.Errorf("DiffTreeFiles parent tree: %w", err)
		}
	}

	diffs, err := object.DiffTree(parentTree, commitTree)
	if err != nil {
		return nil, fmt.Errorf("DiffTreeFiles: %w", err)
	}

	fileSet := make(map[string]struct{})
	for _, ch := range diffs {
		if ch.From.Name != "" {
			fileSet[ch.From.Name] = struct{}{}
		}
		if ch.To.Name != "" {
			fileSet[ch.To.Name] = struct{}{}
		}
	}

	files := make([]string, 0, len(fileSet))
	for f := range fileSet {
		files = append(files, f)
	}
	sort.Strings(files)
	return files, nil
}

func (r *cliRepo) diffTreeFilesCLI(commitSHA string) ([]string, error) {
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
	repo, err := r.open(r.root)
	if err != nil {
		err := r.gitQuiet("diff", "--quiet", sha1, sha2, "--", path)
		if err != nil {
			return true, nil
		}
		return false, nil
	}
	tree1, err := r.commitTree(repo, sha1)
	if err != nil {
		return false, fmt.Errorf("FileChanged: %w", err)
	}
	tree2, err := r.commitTree(repo, sha2)
	if err != nil {
		return false, fmt.Errorf("FileChanged: %w", err)
	}

	entry1, err1 := tree1.FindEntry(path)
	entry2, err2 := tree2.FindEntry(path)

	if err1 != nil && err2 != nil {
		return false, nil
	}
	if err1 != nil || err2 != nil {
		return true, nil
	}
	return !entry1.Hash.Equal(entry2.Hash), nil
}

func (r *cliRepo) RemoteURL(name string) (string, error) {
	repo, err := r.open(r.root)
	if err != nil {
		return r.git("remote", "get-url", name)
	}
	remote, err := repo.Remote(name)
	if err != nil {
		return "", fmt.Errorf("RemoteURL: %w", err)
	}
	urls := remote.Config().URLs
	if len(urls) == 0 {
		return "", fmt.Errorf("RemoteURL: no URLs for remote %q", name)
	}
	return urls[0], nil
}

func (r *cliRepo) RemoteRefExists(remote, branch string) (string, bool, error) {
	repo, err := r.open(r.root)
	if err != nil {
		return r.remoteRefExistsCLI(remote, branch)
	}
	rem, err := repo.Remote(remote)
	if err != nil {
		return "", false, fmt.Errorf("RemoteRefExists: %w", err)
	}
	refs, err := rem.List(&git.ListOptions{})
	if err != nil {
		return "", false, fmt.Errorf("RemoteRefExists: %w", err)
	}
	target := plumbing.ReferenceName("refs/heads/" + branch)
	for _, ref := range refs {
		if ref.Name() == target {
			return ref.Hash().String(), true, nil
		}
	}
	return "", false, nil
}

func (r *cliRepo) remoteRefExistsCLI(remote, branch string) (string, bool, error) {
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
	repo, err := r.open(r.root)
	if err != nil {
		return r.defaultBranchCLI(remote)
	}
	rem, err := repo.Remote(remote)
	if err != nil {
		return "", fmt.Errorf("DefaultBranch: %w", err)
	}
	refs, err := rem.List(&git.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("DefaultBranch: %w", err)
	}
	for _, ref := range refs {
		if ref.Name() == plumbing.HEAD && ref.Type() == plumbing.SymbolicReference {
			return strings.TrimPrefix(string(ref.Target()), "refs/heads/"), nil
		}
	}
	return "", fmt.Errorf("could not determine default branch from %s", remote)
}

func (r *cliRepo) defaultBranchCLI(remote string) (string, error) {
	out, err := r.git("ls-remote", "--symref", remote, "HEAD")
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "ref: ") {
			ref, _, _ := strings.Cut(strings.TrimPrefix(line, "ref: "), "\t")
			return strings.TrimPrefix(ref, "refs/heads/"), nil
		}
	}
	return "", fmt.Errorf("could not determine default branch from %s", remote)
}

func (r *cliRepo) Fetch(remote string, refs ...string) error {
	repo, err := r.open(r.root)
	if err != nil {
		args := append([]string{"fetch", remote}, refs...)
		return r.gitQuiet(args...)
	}
	var refSpecs []config.RefSpec
	for _, ref := range refs {
		refSpecs = append(refSpecs, config.RefSpec(
			fmt.Sprintf("+refs/heads/%s:refs/remotes/%s/%s", ref, remote, ref),
		))
	}
	err = repo.Fetch(&git.FetchOptions{
		RemoteName: remote,
		RefSpecs:   refSpecs,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("Fetch: %w", err)
	}
	return nil
}

func (r *cliRepo) MergeBase(sha1, sha2 string) (string, error) {
	repo, err := r.open(r.root)
	if err != nil {
		return r.git("merge-base", sha1, sha2)
	}
	h1, err := resolveHash(repo, sha1)
	if err != nil {
		return "", fmt.Errorf("MergeBase: %w", err)
	}
	h2, err := resolveHash(repo, sha2)
	if err != nil {
		return "", fmt.Errorf("MergeBase: %w", err)
	}
	c1, err := repo.CommitObject(h1)
	if err != nil {
		return "", fmt.Errorf("MergeBase: %w", err)
	}
	c2, err := repo.CommitObject(h2)
	if err != nil {
		return "", fmt.Errorf("MergeBase: %w", err)
	}
	bases, err := c1.MergeBase(c2)
	if err != nil {
		return "", fmt.Errorf("MergeBase: %w", err)
	}
	if len(bases) == 0 {
		return "", fmt.Errorf("MergeBase: no common ancestor")
	}
	return bases[0].Hash.String(), nil
}

// --- B5: Mutation operations (go-git with CLI fallback) ---

func (r *cliRepo) CommitEmpty(dir, message string) error {
	repo, err := r.open(dir)
	if err != nil {
		return r.gitDir(dir, "commit", "--allow-empty", "-m", message)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("CommitEmpty: %w", err)
	}
	author, err := localAuthor(repo)
	if err != nil {
		return fmt.Errorf("CommitEmpty: %w", err)
	}
	_, err = wt.Commit(message, &git.CommitOptions{
		AllowEmptyCommits: true,
		Author:            author,
	})
	if err != nil {
		return fmt.Errorf("CommitEmpty: %w", err)
	}
	return nil
}

func (r *cliRepo) Push(dir, remote, branch string) error {
	repo, err := r.open(dir)
	if err != nil {
		return r.gitDir(dir, "push", "-u", remote, branch)
	}
	refSpec := config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", branch, branch))
	err = repo.Push(&git.PushOptions{
		RemoteName: remote,
		RefSpecs:   []config.RefSpec{refSpec},
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("Push: %w", err)
	}
	return nil
}

// --- B6: Methods that stay CLI ---

// CLI: go-git lacks three-way merge support
func (r *cliRepo) MergeHasConflicts(base, ours, theirs string) (bool, error) {
	out, err := r.git("merge-tree", base, ours, theirs)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "<<<<<<<"), nil
}

// CLI: go-git has no multi-worktree API
func (r *cliRepo) WorktreeAdd(path, branch, base string) error {
	return r.gitQuiet("worktree", "add", path, "-b", branch, base)
}

// CLI: go-git has no multi-worktree API
func (r *cliRepo) WorktreeRemove(path string) error {
	return r.gitQuiet("worktree", "remove", path, "--force")
}

func (r *cliRepo) WorktreePrune() error {
	gitDir, err := resolveGitDir(r.root)
	if err != nil {
		return err
	}
	return fsWorktreePrune(gitDir)
}

func (r *cliRepo) DeleteBranch(branch string) error {
	gitDir, err := resolveGitDir(r.root)
	if err != nil {
		return err
	}
	return fsDeleteBranch(gitDir, branch)
}

// CLI: worktree-scoped config requires git CLI to handle worktree admin dir correctly
func (r *cliRepo) SetConfig(key, value string) error {
	return r.gitQuiet("config", key, value)
}

// --- Helpers ---

// errStop is a sentinel used to break out of commit iteration.
var errStop = fmt.Errorf("stop")

// resolveHash resolves a ref-like string (hex SHA, "HEAD", "origin/main", etc.)
// to a plumbing.Hash via go-git's ResolveRevision.
func resolveHash(repo *git.Repository, ref string) (plumbing.Hash, error) {
	if h, ok := plumbing.FromHex(ref); ok {
		return h, nil
	}
	h, err := repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return plumbing.Hash{}, fmt.Errorf("resolve %q: %w", ref, err)
	}
	return *h, nil
}

// commitTree returns the tree for a commit identified by hex SHA or symbolic ref.
func (r *cliRepo) commitTree(repo *git.Repository, ref string) (*object.Tree, error) {
	h, err := resolveHash(repo, ref)
	if err != nil {
		return nil, err
	}
	commit, err := repo.CommitObject(h)
	if err != nil {
		return nil, err
	}
	return commit.Tree()
}

// localAuthor reads user.name and user.email from the repo-local config.
func localAuthor(repo *git.Repository) (*object.Signature, error) {
	cfg, err := repo.ConfigScoped(config.LocalScope)
	if err != nil {
		return nil, err
	}
	name := cfg.Author.Name
	email := cfg.Author.Email
	if name == "" {
		name = cfg.User.Name
	}
	if email == "" {
		email = cfg.User.Email
	}
	if name == "" && email == "" {
		return nil, fmt.Errorf("no user.name/user.email in local config")
	}
	return &object.Signature{
		Name:  name,
		Email: email,
		When:  time.Now(),
	}, nil
}
