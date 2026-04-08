// Package setup implements the `runoq init` bootstrap sequence:
// create state directories, resolve GitHub App identity, ensure labels,
// scaffold missing files, and symlink runoq into PATH.
package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	git "github.com/go-git/go-git/v6"

	"github.com/saruman/runoq/internal/config"
	"github.com/saruman/runoq/internal/gh"
	"github.com/saruman/runoq/internal/shell"
)

// Config holds all the parameters needed for a setup run.
type Config struct {
	TargetRoot string
	RunoqRoot  string
	Repo       string
	PlanPath   string
	AppSlug    string
	AppKeyPath string // default: ~/.runoq/app-key.pem
	AppID      int64  // optional override via RUNOQ_APP_ID
	SymlinkDir string // default: /usr/local/bin
	HomeDir    string
	ConfigPath string // path to runoq.json config
	Env        []string // caller-supplied environment
}

// identityData is the structure written to .runoq/identity.json.
type identityData struct {
	AppID          int64  `json:"appId"`
	InstallationID int64  `json:"installationId"`
	PrivateKeyPath string `json:"privateKeyPath"`
}

// installationResponse is the GitHub API response for /repos/{repo}/installation.
type installationResponse struct {
	AppID   int64  `json:"app_id"`
	ID      int64  `json:"id"`
	AppSlug string `json:"app_slug"`
}

// Run executes the full setup sequence.
func Run(ctx context.Context, cfg Config, httpClient *http.Client, exec shell.CommandExecutor, stderr io.Writer) error {
	stateDir := filepath.Join(cfg.TargetRoot, ".runoq", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	if err := ensureIdentity(ctx, cfg, httpClient); err != nil {
		return err
	}

	labels, err := loadLabels(cfg)
	if err != nil {
		return fmt.Errorf("load labels: %w", err)
	}

	ghClient := gh.NewClient(exec, httpClient, operatorEnv(cfg.Env), cfg.TargetRoot, cfg.HomeDir)
	if err := ensureLabels(ctx, cfg.Repo, ghClient, labels); err != nil {
		return fmt.Errorf("ensure labels: %w", err)
	}

	if err := ensureIssueTypes(ctx, cfg.Repo, cfg.TargetRoot, ghClient); err != nil {
		return fmt.Errorf("ensure issue types: %w", err)
	}

	if err := ensurePackageJSON(cfg); err != nil {
		return fmt.Errorf("ensure package.json: %w", err)
	}

	if err := ensureClaudeManagedFiles(cfg); err != nil {
		return fmt.Errorf("ensure claude managed files: %w", err)
	}

	if err := ensureGitignore(cfg); err != nil {
		return fmt.Errorf("ensure gitignore: %w", err)
	}

	if cfg.PlanPath != "" {
		if err := writeProjectConfig(cfg); err != nil {
			return fmt.Errorf("write project config: %w", err)
		}
	}

	if err := ensureSymlink(cfg, stderr); err != nil {
		return fmt.Errorf("ensure symlink: %w", err)
	}

	return nil
}

// operatorEnv returns an env slice without GH_TOKEN/GITHUB_TOKEN and with
// RUNOQ_NO_AUTO_TOKEN set, so gh CLI uses the operator's own credentials.
func operatorEnv(base []string) []string {
	env := append([]string(nil), base...)
	env = shell.EnvSet(env, "RUNOQ_NO_AUTO_TOKEN", "1")
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "GH_TOKEN=") || strings.HasPrefix(e, "GITHUB_TOKEN=") {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

// loadLabels reads the labels section from runoq.json config and returns
// all label values (the strings like "runoq:ready").
func loadLabels(cfg Config) ([]string, error) {
	configPath := config.ResolvePath(cfg.ConfigPath, cfg.RunoqRoot)
	raw, err := config.LoadFile(configPath)
	if err != nil {
		return nil, err
	}
	labelsRaw, ok := raw["labels"]
	if !ok {
		return nil, fmt.Errorf("no labels section in %s", configPath)
	}
	var labelsMap map[string]string
	if err := json.Unmarshal(labelsRaw, &labelsMap); err != nil {
		return nil, fmt.Errorf("parse labels: %w", err)
	}
	labels := make([]string, 0, len(labelsMap))
	for _, v := range labelsMap {
		labels = append(labels, v)
	}
	sort.Strings(labels)
	return labels, nil
}

func ensureIdentity(ctx context.Context, cfg Config, httpClient *http.Client) error {
	identityPath := filepath.Join(cfg.TargetRoot, ".runoq", "identity.json")

	// Check if valid identity already exists.
	if data, err := os.ReadFile(identityPath); err == nil {
		var existing identityData
		if json.Unmarshal(data, &existing) == nil &&
			existing.AppID != 0 && existing.InstallationID != 0 && existing.PrivateKeyPath != "" {
			return nil
		}
	}

	keyPath := cfg.AppKeyPath
	if keyPath == "" {
		keyPath = "~/.runoq/app-key.pem"
	}
	expandedKeyPath := strings.Replace(keyPath, "~", cfg.HomeDir, 1)

	if _, err := os.Stat(expandedKeyPath); err != nil {
		return fmt.Errorf("GitHub App private key not found at %s. Set RUNOQ_APP_KEY or install the key before running runoq init", expandedKeyPath)
	}

	privateKey, err := gh.LoadPrivateKey(expandedKeyPath)
	if err != nil {
		return fmt.Errorf("load private key: %w", err)
	}

	appID := cfg.AppID
	if appID == 0 {
		resolved, err := resolveAppID(ctx, cfg.AppSlug, httpClient)
		if err != nil {
			return err
		}
		appID = resolved
	}

	jwt, err := gh.MintJWT(appID, privateKey)
	if err != nil {
		return fmt.Errorf("mint JWT: %w", err)
	}

	installation, err := resolveInstallation(ctx, cfg.Repo, jwt, httpClient)
	if err != nil {
		return err
	}

	if cfg.AppSlug != "" && installation.AppSlug != "" && installation.AppSlug != cfg.AppSlug {
		return fmt.Errorf("repository installation app slug %s did not match configured identity.appSlug %s",
			installation.AppSlug, cfg.AppSlug)
	}

	identity := identityData{
		AppID:          installation.AppID,
		InstallationID: installation.ID,
		PrivateKeyPath: keyPath,
	}

	data, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(identityPath, append(data, '\n'), 0o644)
}

func resolveAppID(ctx context.Context, slug string, httpClient *http.Client) (int64, error) {
	url := fmt.Sprintf("https://api.github.com/apps/%s", slug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "runoq-runtime")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("resolve app ID for %s: %w", slug, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("unable to resolve app ID for %s. For private GitHub Apps, set RUNOQ_APP_ID before running runoq init", slug)
	}

	var result struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result.ID, nil
}

func resolveInstallation(ctx context.Context, repo, jwt string, httpClient *http.Client) (*installationResponse, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/installation", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "runoq-runtime")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("resolve installation for %s: %w", repo, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("GitHub App installation not found for %s. Install the app on this repository, then rerun runoq init", repo)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("failed to resolve GitHub App installation for %s: HTTP %d", repo, resp.StatusCode)
	}

	var installation installationResponse
	if err := json.NewDecoder(resp.Body).Decode(&installation); err != nil {
		return nil, err
	}
	return &installation, nil
}

func ensureLabels(ctx context.Context, repo string, ghClient *gh.Client, labels []string) error {
	// Retry label listing — newly created repos may not be immediately
	// accessible via the GraphQL endpoint that gh label list uses.
	var out string
	var listErr error
	for attempt := 1; attempt <= 5; attempt++ {
		out, listErr = ghClient.Output(ctx, "label", "list", "--repo", repo, "--limit", "200", "--json", "name")
		if listErr == nil {
			break
		}
		if attempt < 5 {
			time.Sleep(3 * time.Second)
		}
	}
	if listErr != nil {
		return fmt.Errorf("list labels: %w", listErr)
	}

	var existing []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &existing); err != nil {
		return fmt.Errorf("parse label list: %w", err)
	}

	existingSet := make(map[string]bool, len(existing))
	for _, l := range existing {
		existingSet[l.Name] = true
	}

	for _, label := range labels {
		if existingSet[label] {
			continue
		}
		var lastErr error
		for attempt := 1; attempt <= 3; attempt++ {
			_, err := ghClient.Output(ctx, "label", "create", label, "--repo", repo, "--color", "BFDADC", "--description", "Managed by runoq")
			if err == nil {
				lastErr = nil
				break
			}
			lastErr = err
		}
		if lastErr != nil {
			return fmt.Errorf("create label %s: %w", label, lastErr)
		}
	}
	return nil
}

func ensureIssueTypes(ctx context.Context, repo string, targetRoot string, ghClient *gh.Client) error {
	org, _, ok := strings.Cut(repo, "/")
	if !ok {
		return fmt.Errorf("invalid repo format %q", repo)
	}

	query := fmt.Sprintf(`query { organization(login: %q) { issueTypes(first: 20) { nodes { name id } } } }`, org)
	raw, err := ghClient.Output(ctx, "api", "graphql", "-f", "query="+query)
	if err != nil {
		return fmt.Errorf("query org issue types: %w", err)
	}

	var resp struct {
		Data struct {
			Organization struct {
				IssueTypes struct {
					Nodes []struct {
						Name string `json:"name"`
						ID   string `json:"id"`
					} `json:"nodes"`
				} `json:"issueTypes"`
			} `json:"organization"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return fmt.Errorf("parse issue types response: %w", err)
	}

	mapping := make(map[string]string)
	for _, it := range resp.Data.Organization.IssueTypes.Nodes {
		mapping[strings.ToLower(it.Name)] = it.ID
	}

	var missing []string
	for _, required := range []string{"task", "epic"} {
		if _, ok := mapping[required]; !ok {
			missing = append(missing, strings.ToUpper(required[:1])+required[1:])
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("org %s is missing required issue types: %s. Enable them in org settings", org, strings.Join(missing, ", "))
	}

	data, err := json.MarshalIndent(mapping, "", "  ")
	if err != nil {
		return err
	}
	issueTypesPath := filepath.Join(targetRoot, ".runoq", "issue-types.json")
	if err := os.MkdirAll(filepath.Dir(issueTypesPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(issueTypesPath, append(data, '\n'), 0o644)
}

func ensurePackageJSON(cfg Config) error {
	pkgPath := filepath.Join(cfg.TargetRoot, "package.json")
	if _, err := os.Stat(pkgPath); err == nil {
		return nil
	}

	content := `{
  "name": "runoq-target",
  "private": true,
  "scripts": {
    "test": "echo \"No tests configured\"",
    "build": "echo \"No build configured\""
  }
}
`
	if err := os.WriteFile(pkgPath, []byte(content), 0o644); err != nil {
		return err
	}
	return goGitAdd(cfg.TargetRoot, "package.json")
}

func ensureClaudeManagedFiles(cfg Config) error {
	agentsSrc := filepath.Join(cfg.RunoqRoot, ".claude", "agents")
	skillsSrc := filepath.Join(cfg.RunoqRoot, ".claude", "skills")
	agentsDst := filepath.Join(cfg.TargetRoot, ".claude", "agents")
	skillsDst := filepath.Join(cfg.TargetRoot, ".claude", "skills")

	if err := os.MkdirAll(agentsDst, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(skillsDst, 0o755); err != nil {
		return err
	}

	if err := syncManagedTree(agentsSrc, agentsDst); err != nil {
		return err
	}
	return syncManagedTree(skillsSrc, skillsDst)
}

func syncManagedTree(srcRoot, dstRoot string) error {
	var paths []string
	err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	sort.Strings(paths)

	for _, srcPath := range paths {
		relPath, err := filepath.Rel(srcRoot, srcPath)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstRoot, relPath)

		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}

		info, err := os.Lstat(dstPath)
		if err == nil {
			if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("cannot update managed Claude file at %s; path exists and is not a regular file", dstPath)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(dstPath)
				if err == nil && target == srcPath {
					continue
				}
			}
		}

		_ = os.Remove(dstPath)
		if err := os.Symlink(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

func ensureGitignore(cfg Config) error {
	gitignorePath := filepath.Join(cfg.TargetRoot, ".gitignore")
	entry := ".runoq/"

	data, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	for _, line := range strings.Split(string(data), "\n") {
		if line == entry {
			return nil
		}
	}

	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, []byte(entry+"\n")...)

	if err := os.WriteFile(gitignorePath, data, 0o644); err != nil {
		return err
	}
	return goGitAdd(cfg.TargetRoot, ".gitignore")
}

func writeProjectConfig(cfg Config) error {
	configPath := filepath.Join(cfg.TargetRoot, "runoq.json")

	var doc map[string]any
	if data, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("failed to update %s; expected valid JSON", configPath)
		}
	} else {
		doc = make(map[string]any)
	}

	doc["plan"] = cfg.PlanPath

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return goGitAdd(cfg.TargetRoot, "runoq.json")
}

func ensureSymlink(cfg Config, stderr io.Writer) error {
	linkDir := cfg.SymlinkDir
	if linkDir == "" {
		linkDir = "/usr/local/bin"
	}

	linkPath := filepath.Join(linkDir, "runoq")
	target := filepath.Join(cfg.RunoqRoot, "bin", "runoq")

	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "Warning: %s is not writable. Set RUNOQ_SYMLINK_DIR to a writable directory on your PATH.\n", linkDir)
		return nil
	}

	if linfo, err := os.Lstat(linkPath); err == nil {
		if linfo.Mode()&os.ModeSymlink != 0 {
			existing, err := os.Readlink(linkPath)
			if err == nil && existing == target {
				return nil
			}
			_ = os.Remove(linkPath)
		} else {
			return fmt.Errorf("cannot update %s; file exists and is not a symlink", linkPath)
		}
	}

	return os.Symlink(target, linkPath)
}

// goGitAdd stages a file using go-git's worktree API.
func goGitAdd(repoRoot string, relPath string) error {
	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("open repo for staging %s: %w", relPath, err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("worktree for staging %s: %w", relPath, err)
	}
	_, err = wt.Add(relPath)
	return err
}
