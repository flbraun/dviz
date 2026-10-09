// Package app wires configuration, hubs and the HTTP server together and applies
// configuration changes at runtime.
package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/flbraun/dviz/internal/config"
	"github.com/flbraun/dviz/internal/hub"
	"github.com/flbraun/dviz/internal/server"
)

// Options configures Run.
type Options struct {
	// Candidates is the config file search order (see config.DefaultCandidates).
	Candidates []string
	// Assets is the frontend served at /.
	Assets fs.FS
	// OnListen, if set, is called with the bound address whenever a listener starts.
	OnListen func(addr string)
}

// Run serves dviz until ctx is done.
func Run(ctx context.Context, opts Options) error {
	cfg, err := config.Load(opts.Candidates)
	if err != nil {
		return err
	}
	if cfg.Source != "" {
		slog.Info("config loaded", "source", cfg.Source)
	} else {
		slog.Info("no config file found, using defaults", "searched", opts.Candidates)
	}

	reg := hub.NewRegistry(ctx)
	defer reg.Close()
	reg.Apply(cfg.Hosts)

	srv := server.New(reg, opts.Assets)
	ln, err := listen(cfg.Listen, srv, opts.OnListen)
	if err != nil {
		return err
	}
	defer func() { ln.stop() }()

	updates := make(chan *config.Config)
	watchErr := make(chan error, 1)
	go func() {
		watchErr <- config.Watch(ctx, opts.Candidates, cfg, func(c *config.Config) {
			select {
			case updates <- c:
			case <-ctx.Done():
			}
		})
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-watchErr:
			if err != nil {
				return fmt.Errorf("config watcher: %w", err)
			}
		case next := <-updates:
			reg.Apply(next.Hosts)
			if next.Listen != cfg.Listen {
				nl, err := listen(next.Listen, srv, opts.OnListen)
				if err != nil {
					slog.Error("cannot switch listen address, keeping the current one", "listen", next.Listen, "err", err)
					next.Listen = cfg.Listen
				} else {
					ln.stop()
					ln = nl
				}
			}
			cfg = next
		}
	}
}

type listener struct {
	srv    *http.Server
	cancel context.CancelFunc
	addr   string
}

func listen(addr string, srv *server.Server, onListen func(string)) (*listener, error) {
	nl, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	// Cancelling base cancels all request contexts, which ends long-lived SSE streams so
	// that Shutdown completes promptly.
	base, cancel := context.WithCancel(context.Background())
	hs := &http.Server{
		Handler:           srv.Handler(isLoopback(addr)),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return base },
	}
	l := &listener{srv: hs, cancel: cancel, addr: nl.Addr().String()}
	go func() {
		if err := hs.Serve(nl); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server stopped", "err", err)
		}
	}()
	slog.Info("listening", "url", "http://"+l.addr)
	if onListen != nil {
		onListen(l.addr)
	}
	return l, nil
}

func (l *listener) stop() {
	l.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.srv.Shutdown(ctx); err != nil {
		_ = l.srv.Close()
	}
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
