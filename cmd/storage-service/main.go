package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
 "net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/agent"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/config"
 "github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/filews"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/httpserver"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/identity"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/inventory"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/sourcews"
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
	var reader *sourcews.Service
	if cfg.SourceWS.Enabled {
		peers, err := sourcews.ParsePeerIPs(cfg.SourceWS.AllowedGatewayIPs)
		if err != nil { slog.Error("source WS peers invalid", "error", err); os.Exit(1) }
		reader = &sourcews.Service{NodeID: cfg.StorageNodeID, Catalog: c, AllowedPeers: peers}
	}
	server := &http.Server{Addr: cfg.HTTPListen, Handler: httpserver.HandlerWithSource(cfg.StorageNodeID, c, reader, control.Ready), ReadHeaderTimeout: 5 * time.Second}
	// File transfer is always available on detected private interfaces. The
	// host firewall controls reachability; filesystem roots control operations.
	// Obsolete file_ws JSON settings are ignored (no flags, tokens or IP ACLs).
	privateIPs, err := filews.PrivateListenerIPv4()
	if err != nil {
		slog.Error("file transfer interface discovery failed", "error", err)
		os.Exit(1)
	}
	fileServer := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	stream := &filews.Service{Catalog: c, Slots: make(chan struct{}, 2)}
	mux := http.NewServeMux()
	mux.Handle("GET /ws/files", stream)
	fileServer.Handler = mux
	var listeners []net.Listener
	for _, ip := range privateIPs {
		address := net.JoinHostPort(ip, "8093")
		listener, listenErr := net.Listen("tcp", address)
		if listenErr != nil {
			slog.Warn("file transfer private interface unavailable", "listen", address, "error", listenErr)
			continue
		}
		listeners = append(listeners, listener)
		control.FileWSEndpoints = append(control.FileWSEndpoints, "ws://"+address+"/ws/files")
	}
	if len(listeners) == 0 {
		slog.Error("no usable private interface for file transfer on port 8093")
		os.Exit(1)
	}
	for _, listener := range listeners {
		ln := listener
		go func() {
			if serveErr := fileServer.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				slog.Error("file transfer listener stopped", "address", ln.Addr().String(), "error", serveErr)
				stop()
			}
		}()
	}
	slog.Info("HC SS automatic file transfer enabled", "endpoints", control.FileWSEndpoints)
	// The control connection only carries inventory and metadata, not file data.
	go control.Run(ctx)
  	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
        if fileServer!=nil {_ = fileServer.Shutdown(shutdownCtx)}
	}()
	slog.Info("HC Storage Service started", "node_id", cfg.StorageNodeID, "listen", cfg.HTTPListen, "roots", len(cfg.Roots), "backend_ws_url", cfg.Backend.ControlWSURL)
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("HTTP server failed", "error", err)
		os.Exit(1)
	}
}
