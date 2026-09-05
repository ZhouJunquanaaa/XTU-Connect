package hook_func

import (
	"context"
	"os"

	"xtu-connect/configs"
)

func init() {
	RegisterInitialFunc("clean resolver file", func(ctx context.Context, config configs.Config) error {
		// discard error
		_ = os.Remove("/etc/resolver/xtu.edu.cn")
		_ = os.Remove("/etc/resolver/cc98.org")
		return nil
	})
	//RegisterInitialFunc("check bind port", checkBindPortLegal) // TODO: figure out whether to check port or not
}
