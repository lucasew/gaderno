package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/lucasew/gaderno/internal/app"
	"github.com/lucasew/gaderno/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the gaderno HTTP server",
	RunE:  runServe,
}

func init() {
	// Empty defaults so unset flags do not mask env (flags > env > defaults).
	serveCmd.Flags().String("root", "", "workspace root directory (env GADERNO_ROOT)")
	serveCmd.Flags().String("listen", "", "listen address (env GADERNO_LISTEN)")
	serveCmd.Flags().String("token", "", "shared access token (env GADERNO_TOKEN)")
	serveCmd.Flags().String("kernel", "", "default kernelspec name (env GADERNO_KERNEL)")

	_ = viper.BindPFlag("root", serveCmd.Flags().Lookup("root"))
	_ = viper.BindPFlag("listen", serveCmd.Flags().Lookup("listen"))
	_ = viper.BindPFlag("token", serveCmd.Flags().Lookup("token"))
	_ = viper.BindPFlag("kernel", serveCmd.Flags().Lookup("kernel"))

	viper.SetDefault("root", ".")
	viper.SetDefault("listen", "127.0.0.1:8080")
	viper.SetDefault("token", "")
	viper.SetDefault("kernel", "python3")
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg := config.Config{
		Root:   viper.GetString("root"),
		Listen: viper.GetString("listen"),
		Token:  viper.GetString("token"),
		Kernel: viper.GetString("kernel"),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, cfg, version); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
