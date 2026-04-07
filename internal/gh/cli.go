package gh

import (
	"context"

	"github.com/saruman/runoq/internal/shell"
)

func Output(ctx context.Context, exec shell.CommandExecutor, cwd string, env []string, args ...string) (string, error) {
	bin := "gh"
	if v, ok := shell.EnvLookup(env, "GH_BIN"); ok && v != "" {
		bin = v
	}
	return shell.CommandOutput(ctx, exec, shell.CommandRequest{
		Name: bin,
		Args: args,
		Dir:  cwd,
		Env:  env,
	})
}
