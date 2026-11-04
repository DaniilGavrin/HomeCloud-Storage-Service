// Package httpserver contains local diagnostics for the initial HC SS scaffold.
// Its HTTP handlers are NOT the planned WebSocket storage protocol.
package httpserver

import (
	"encoding/json"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
	"net/http"
)

func Handler(storageNodeID string, c *catalog.Catalog) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "alive", "storage_node_id": storageNodeID, "phase": "scaffold"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "not_ready", "root_count": len(c.Published()), "ws_ready": false})
	})
	return mux
}
