package httpserver

import (
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthAndReadiness(t *testing.T) {
	c, _ := catalog.New(nil)
	h := Handler("test-node", c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "test-node") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"ws_ready":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestReadinessTurnsReadyAfterBackendRegistration(t *testing.T) {
	c, _ := catalog.New(nil)
	ready := false
	h := Handler("node", c, func() bool { return ready })
	ready = true
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ws_ready":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
