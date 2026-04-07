package gh

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/saruman/runoq/internal/gitops"
	"github.com/saruman/runoq/internal/shell"
)

// identityFile represents the .runoq/identity.json configuration.
type identityFile struct {
	AppID          int64  `json:"appId"`
	InstallationID int64  `json:"installationId"`
	PrivateKeyPath string `json:"privateKeyPath"`
}

// Client wraps gh CLI execution with automatic token lifecycle management.
type Client struct {
	exec       shell.CommandExecutor
	httpClient *http.Client
	env        []string
	cwd        string
	tokenInit  bool
}

// NewClient creates a Client with the given executor, HTTP client, environment, and working directory.
func NewClient(exec shell.CommandExecutor, httpClient *http.Client, env []string, cwd string) *Client {
	return &Client{
		exec:       exec,
		httpClient: httpClient,
		env:        env,
		cwd:        cwd,
	}
}

// EnsureToken checks for GH_TOKEN, reads identity.json, and mints a token if needed.
// Modifies c.env on success. Returns nil on success or silent failure if no identity is found.
func (c *Client) EnsureToken(ctx context.Context) error {
	if _, ok := shell.EnvLookup(c.env, "GH_TOKEN"); ok {
		return nil
	}
	if _, ok := shell.EnvLookup(c.env, "RUNOQ_NO_AUTO_TOKEN"); ok {
		return nil
	}
	if c.tokenInit {
		return nil
	}
	c.tokenInit = true

	targetRoot, err := gitops.FindRoot(c.cwd)
	if err != nil {
		return nil
	}

	identityPath := filepath.Join(targetRoot, ".runoq", "identity.json")
	data, err := os.ReadFile(identityPath)
	if err != nil {
		return nil
	}

	var identity identityFile
	if err := json.Unmarshal(data, &identity); err != nil {
		return nil
	}
	if identity.AppID == 0 || identity.InstallationID == 0 || identity.PrivateKeyPath == "" {
		return nil
	}

	keyPath := strings.Replace(identity.PrivateKeyPath, "~", os.Getenv("HOME"), 1)
	privateKey, err := LoadPrivateKey(keyPath)
	if err != nil {
		return nil
	}

	token, err := MintBotToken(c.httpClient, identity.AppID, identity.InstallationID, privateKey)
	if err != nil || token == "" {
		return nil
	}

	c.env = shell.EnvSet(c.env, "GH_TOKEN", token)
	return nil
}

// Env returns the current environment, which may include a minted GH_TOKEN.
func (c *Client) Env() []string {
	return c.env
}

// Output runs gh with the given args and returns trimmed stdout.
// EnsureToken is called before execution.
func (c *Client) Output(ctx context.Context, args ...string) (string, error) {
	if err := c.EnsureToken(ctx); err != nil {
		return "", err
	}
	bin := "gh"
	if v, ok := shell.EnvLookup(c.env, "GH_BIN"); ok && v != "" {
		bin = v
	}
	return shell.CommandOutput(ctx, c.exec, shell.CommandRequest{
		Name: bin,
		Args: args,
		Dir:  c.cwd,
		Env:  c.env,
	})
}

// Run runs gh with the given args and explicit IO writers.
// EnsureToken is called before execution.
func (c *Client) Run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	if err := c.EnsureToken(ctx); err != nil {
		return err
	}
	bin := "gh"
	if v, ok := shell.EnvLookup(c.env, "GH_BIN"); ok && v != "" {
		bin = v
	}
	return c.exec(ctx, shell.CommandRequest{
		Name:   bin,
		Args:   args,
		Dir:    c.cwd,
		Env:    c.env,
		Stdout: stdout,
		Stderr: stderr,
	})
}
