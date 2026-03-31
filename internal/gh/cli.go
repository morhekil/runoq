package gh

import (
	"context"

	"github.com/saruman/runoq/internal/common"
)

func Output(ctx context.Context, exec common.CommandExecutor, cwd string, env []string, args ...string) (string, error) {
	bin := "gh"
	if v, ok := common.EnvLookup(env, "GH_BIN"); ok && v != "" {
		bin = v
	}
	return common.CommandOutput(ctx, exec, common.CommandRequest{
		Name: bin,
		Args: args,
		Dir:  cwd,
		Env:  env,
	})
}
