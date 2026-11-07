package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
 "github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/filews"
	"os"
	"strings"
	"net"
	"net/netip"
 "path/filepath"

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
	FileWS struct {
    Enabled bool `json:"enabled"`
    Listen string `json:"listen"`
    AuthMode string `json:"auth_mode"` // token (default) or trusted_network
    TokenFile string `json:"token_file"`
    AllowedClientIPs []string `json:"allowed_client_ips"`
    MaxUploadBytes int64 `json:"max_upload_bytes"`
  } `json:"file_ws"`
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
if cfg.FileWS.Enabled {
  host,port,err:=net.SplitHostPort(cfg.FileWS.Listen)
  if err!=nil || host==""||port=="" {return Config{},errors.New("FILE_WS_LISTEN_REQUIRED")}
  addr,err:=netip.ParseAddr(host)
  tailnet:=netip.MustParsePrefix("100.64.0.0/10")
  if err!=nil||!addr.Is4()||!(tailnet.Contains(addr)||addr.IsPrivate()||addr.IsLoopback()) {return Config{},errors.New("FILE_WS_REQUIRES_EXPLICIT_PRIVATE_BIND")}
  if _,err:=net.LookupPort("tcp",port);err!=nil {return Config{},errors.New("FILE_WS_INVALID_PORT")}
  if cfg.FileWS.Listen==cfg.HTTPListen{return Config{},errors.New("FILE_WS_SEPARATE_LISTENER_REQUIRED")}
  if cfg.FileWS.MaxUploadBytes<0||cfg.FileWS.MaxUploadBytes>1<<40{return Config{},errors.New("FILE_WS_INVALID_MAX_UPLOAD")}
  if cfg.FileWS.AuthMode=="" {cfg.FileWS.AuthMode="token"}
  if cfg.FileWS.AuthMode!="token" && cfg.FileWS.AuthMode!="trusted_network" {return Config{},errors.New("FILE_WS_INVALID_AUTH_MODE")}
  // Trusted private LAN needs no per-client static IPs. A configured list
  // still narrows access to those exact addresses.
  if cfg.FileWS.AuthMode=="token" && len(cfg.FileWS.AllowedClientIPs)==0 {return Config{},errors.New("FILE_WS_ALLOWED_CLIENT_IPS_REQUIRED")}
  if len(cfg.FileWS.AllowedClientIPs)>0 {
    if _,err:=filews.ParseAllowedIPs(cfg.FileWS.AllowedClientIPs);err!=nil{return Config{},err}
  }
  if cfg.FileWS.AuthMode=="token" && cfg.FileWS.TokenFile=="" {cfg.FileWS.TokenFile=filepath.Join(cfg.DataDir,"filews-token")}
 }
 	return cfg, nil
}
