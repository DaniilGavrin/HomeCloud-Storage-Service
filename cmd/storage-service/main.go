package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/config"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/httpserver"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/identity"
)

func main() {
	path := flag.String("config", "config.example.json", "Local HC SS configuration")
	flag.Parse()
	cfg, err := config.Load(*path)
	if err != nil {
		slog.Error("configuration failed", "error", err)
		os.Exit(1)
	}
	if cfg.StorageNodeID == "" {
		cfg.StorageNodeID, err = identity.LoadOrCreate(filepath.Join(cfg.DataDir, "storage-node-id"))
		if err != nil {
			slog.Error("identity failed", "error", err)
			os.Exit(1)
		}
	}
	c, err := catalog.New(cfg.Roots)
	if err != nil {
		slog.Error("roots invalid", "error", err)
		os.Exit(1)
	}
	// The first stage deliberately exposes health diagnostics only.
	// WS Backend client and /ws/source server are subsequent KAN-39 tasks.
	server := &http.Server{Addr: cfg.HTTPListen, Handler: httpserver.Handler(cfg.StorageNodeID, c), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("HC Storage Service scaffold started", "node_id", cfg.StorageNodeID, "listen", cfg.HTTPListen, "roots", len(cfg.Roots), "control_ws_connected", false)
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("HTTP server failed", "error", err)
		os.Exit(1)
	}
}
