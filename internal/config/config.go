package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
	"os"
	"strings"
	"net"
	"net/netip"

	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/sourcews"
)

type Config struct {
	Version       int    `json:"config_version"`
	DataDir       string `json:"data_dir"`
	StorageNodeID string `json:"storage_node_id"`
	HTTPListen    string `json:"http_listen"`
	SourceWS struct {
		Enabled bool `json:"enabled"`
		AllowedGatewayIPs []string `json:"allowed_gateway_ips"`
	} `json:"source_ws"`
	// FileWS accepts obsolete pre-automatic configuration without applying it.
	// Existing installations need no JSON migration or manual listener setup.
	FileWS json.RawMessage `json:"file_ws,omitempty"`
  Backend       struct {
		ControlWSURL string `json:"control_ws_url"`
	} `json:"backend"`
	Roots []catalog.Root `json:"roots"`
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil {
		return Config{}, err
	}
	if st.Size() > 1<<20 {
		return Config{}, errors.New("CONFIG_TOO_LARGE")
	}
	dec := json.NewDecoder(file)
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("config parse: %w", err)
	}
	if cfg.Version != 1 {
		return Config{}, errors.New("CONFIG_VERSION_UNSUPPORTED")
	}
	if cfg.StorageNodeID != "" && strings.TrimSpace(cfg.StorageNodeID) != cfg.StorageNodeID {
		return Config{}, errors.New("INVALID_STORAGE_NODE_ID")
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.HTTPListen == "" {
		cfg.HTTPListen = "127.0.0.1:8092"
	}
	if _, err := catalog.New(cfg.Roots); err != nil {
		return Config{}, err
	}
	if cfg.SourceWS.Enabled {
		host, _, err := net.SplitHostPort(cfg.HTTPListen)
		if err != nil { return Config{}, fmt.Errorf("invalid http_listen: %w", err) }
		addr, err := netip.ParseAddr(host)
		if err != nil || addr.IsUnspecified() || addr.IsMulticast() ||
			!(addr.IsPrivate() || addr.IsLoopback() || netip.MustParsePrefix("100.64.0.0/10").Contains(addr.Unmap())) {
			return Config{}, errors.New("source_ws requires an explicit private listener address")
		}
		allowed, err := sourcews.ParsePeerIPs(cfg.SourceWS.AllowedGatewayIPs)
		if err != nil || len(allowed) == 0 {
			return Config{}, errors.New("source_ws requires at least one exact private allowed_gateway_ips")
		}
	}

 	return cfg, nil
}
