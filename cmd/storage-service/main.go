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

	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/agent"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/config"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/httpserver"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/identity"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/inventory"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	control := &agent.Control{URL: cfg.Backend.ControlWSURL, NodeID: cfg.StorageNodeID, Catalog: c, Inspector: inventory.OSInspector{}, Logger: slog.Default()}
	// Control uses only the existing Backend WS. No passwords, certificates,
	// API keys or extra user authorisation.
	go control.Run(ctx)
	server := &http.Server{Addr: cfg.HTTPListen, Handler: httpserver.Handler(cfg.StorageNodeID, c, control.Ready), ReadHeaderTimeout: 5 * time.Second}
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("HC Storage Service started", "node_id", cfg.StorageNodeID, "listen", cfg.HTTPListen, "roots", len(cfg.Roots), "backend_ws_url", cfg.Backend.ControlWSURL)
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("HTTP server failed", "error", err)
		os.Exit(1)
	}
}
