package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
	"os"
	"strings"
)

type Config struct {
	Version       int    `json:"config_version"`
	DataDir       string `json:"data_dir"`
	StorageNodeID string `json:"storage_node_id"`
	HTTPListen    string `json:"http_listen"`
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
	return cfg, nil
}
