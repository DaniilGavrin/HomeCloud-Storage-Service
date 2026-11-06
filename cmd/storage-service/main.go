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
	// Control uses only the existing Backend WS. No passwords, certificates,
	// API keys or extra user authorisation.
	go control.Run(ctx)
	var reader *sourcews.Service
	if cfg.SourceWS.Enabled {
		peers, err := sourcews.ParsePeerIPs(cfg.SourceWS.AllowedGatewayIPs)
		if err != nil { slog.Error("source WS peers invalid", "error", err); os.Exit(1) }
		reader = &sourcews.Service{NodeID: cfg.StorageNodeID, Catalog: c, AllowedPeers: peers}
	}
	server := &http.Server{Addr: cfg.HTTPListen, Handler: httpserver.HandlerWithSource(cfg.StorageNodeID, c, reader, control.Ready), ReadHeaderTimeout: 5 * time.Second}
	// File data and mutations use a separate opt-in Tailnet-only listener.
    // The token never traverses Backend WS, source WS, inventory or logs.
    var fileServer *http.Server
    if cfg.FileWS.Enabled {
      peers,err:=filews.ParseAllowedIPs(cfg.FileWS.AllowedClientIPs)
      if err!=nil{slog.Error("file ws allowed peers invalid", "error",err);os.Exit(1)}
      key,err:=filews.LoadOrCreateToken(cfg.FileWS.TokenFile)
      if err!=nil{slog.Error("file ws credential setup failed", "error",err);os.Exit(1)}
      stream:=&filews.Service{Catalog:c,TokenSHA256:key,AllowedPeers:peers,MaxUpload:cfg.FileWS.MaxUploadBytes,Slots:make(chan struct{},2)}
      mux:=http.NewServeMux()
      mux.Handle("GET /ws/files",stream)
      fileServer=&http.Server{Addr:cfg.FileWS.Listen,Handler:mux,ReadHeaderTimeout:5*time.Second}
      listener,err:=net.Listen("tcp",cfg.FileWS.Listen)
      if err!=nil{slog.Error("file ws listener failed", "error",err);os.Exit(1)}
      go func(){
        if e:=fileServer.Serve(listener);e!=nil&&!errors.Is(e,http.ErrServerClosed){
          slog.Error("file ws server failed", "error",e)
          stop()
        }
      }()
      slog.Info("HC SS file transfer listener enabled", "listen",cfg.FileWS.Listen)
    }
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
