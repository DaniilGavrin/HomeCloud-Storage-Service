// Package httpserver contains local diagnostics for the initial HC SS scaffold.
// Its HTTP handlers are NOT the planned WebSocket storage protocol.
package httpserver

import (
	"encoding/json"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
	"net/http"
)

func Handler(storageNodeID string, c *catalog.Catalog, readyCheck ...func() bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "alive", "storage_node_id": storageNodeID, "phase": "control_ws_v1"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		connected := false
		if len(readyCheck) > 0 && readyCheck[0] != nil {
			connected = readyCheck[0]()
		}
		w.Header().Set("Content-Type", "application/json")
		if !connected {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		status := "ready"
		if !connected {
			status = "not_ready"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "root_count": len(c.Published()), "ws_ready": connected})
	})
	return mux
}
