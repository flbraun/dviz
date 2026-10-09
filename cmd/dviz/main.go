// Command dviz serves an interactive 3D view of Docker hosts.
//
// dviz takes no command-line flags; it is configured through dviz.yml, searched in
// $(pwd)/dviz.yml, ~/.config/dviz/dviz.yml and /etc/dviz/dviz.yml.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/flbraun/dviz/internal/app"
	"github.com/flbraun/dviz/internal/config"
	"github.com/flbraun/dviz/web"
)

// version is set at release build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		fmt.Fprintln(os.Stderr, "dviz takes no arguments; configure it via dviz.yml in:")
		for _, c := range config.DefaultCandidates() {
			fmt.Fprintln(os.Stderr, "  "+c)
		}
		os.Exit(2)
	}

	slog.Info("starting dviz", "version", version)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := app.Run(ctx, app.Options{Candidates: config.DefaultCandidates(), Assets: web.Assets()})
	if err != nil {
		slog.Error("dviz failed", "err", err)
		os.Exit(1)
	}
}
