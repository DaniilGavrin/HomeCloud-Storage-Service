// Package agent implements HC Storage Service's outgoing WebSocket control
// connection to HomeCloudBackend. No service authentication is performed;
// storage_node_id is a non-secret routing label. No source bytes cross it.
package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/inventory"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/sourcews"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/wsclient"
)

type Control struct {
	URL       string
	NodeID    string
	FileWSEndpoints []string // auto-detected private data-plane addresses; never credentials
	Catalog   *catalog.Catalog
	Inspector inventory.Inspector
	Logger    *slog.Logger
	Connected atomic.Bool
	lastError atomic.Value // string; initialized before goroutine
	epoch     atomic.Uint64
}

type message struct {
	V          int             `json:"v"`
	ID         string          `json:"id,omitempty"`
	Type       string          `json:"type"`
	Action     string          `json:"action,omitempty"`
	Topic      string          `json:"topic,omitempty"`
	DeviceID   string          `json:"device_id,omitempty"`
	ClientType string          `json:"client_type,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Error      string          `json:"error,omitempty"`
}

func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func send(ctx context.Context, c *wsclient.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteText(ctx, b)
}
func recv(ctx context.Context, c *wsclient.Conn) (message, error) {
	var m message
	b, err := c.ReadText(ctx)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	if m.V != 1 || m.Type == "" {
		return m, errors.New("invalid Backend response")
	}
	return m, nil
}
func expect(ctx context.Context, c *wsclient.Conn, id, typ string) (message, error) {
	for {
		m, err := recv(ctx, c)
		if err != nil {
			return m, err
		}
		if m.ID != id {
			return m, fmt.Errorf("unexpected Backend reply %q (wanted %q)", m.ID, id)
		}
		if m.Type == "error" {
			return m, fmt.Errorf("Backend rejected %q: %s", id, m.Error)
		}
		if m.Type != typ {
			return m, fmt.Errorf("unexpected reply type %s", m.Type)
		}
		return m, nil
	}
}
func bootID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func (a *Control) Ready() bool { return a != nil && a.Connected.Load() }
func (a *Control) LastError() string {
	if a == nil {
		return ""
	}
	x := a.lastError.Load()
	if x == nil {
		return ""
	}
	return x.(string)
}
func (a *Control) Run(ctx context.Context) {
	if a == nil {
		return
	}
	if a.Logger == nil {
		a.Logger = slog.Default()
	}
	a.lastError.Store("")
	if _, err := url.Parse(a.URL); err != nil || a.NodeID == "" || a.Catalog == nil || a.Inspector == nil {
		a.lastError.Store("missing control configuration")
		return
	}
	backoff := time.Second
	for ctx.Err() == nil {
		err := a.session(ctx)
		a.Connected.Store(false)
		if ctx.Err() != nil {
			return
		}
		a.lastError.Store(err.Error())
		a.Logger.Warn("HC SS Backend WS disconnected; retrying", "error", err, "retry_after", backoff)
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}
func (a *Control) session(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, err := wsclient.Dial(dialCtx, a.URL)
	cancel()
	if err != nil {
		return err
	}
	defer conn.Close()
	cleanup := context.AfterFunc(ctx, func() { conn.Close() })
	defer cleanup()
	id, err := bootID()
	if err != nil {
		return err
	}
	handshake, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := send(handshake, conn, map[string]any{"v": 1, "type": "hello", "id": "hc-ss-hello", "device_id": a.NodeID, "client_type": "storage_service"}); err != nil {
		return err
	}
	if _, err = expect(handshake, conn, "hc-ss-hello", "identified"); err != nil {
		return fmt.Errorf("Backend hello: %w", err)
	}
	register := map[string]any{"v": 1, "id": "hc-ss-register", "type": "command", "action": "storage.agent.register", "data": map[string]any{"storage_node_id": a.NodeID, "boot_id": id, "protocol_version": 1, "capabilities": []string{"volume_inventory", "root_inventory", "fs_list", "fs_stat"}}}
	if err := send(handshake, conn, register); err != nil {
		return err
	}
	reply, err := expect(handshake, conn, "hc-ss-register", "result")
	if err != nil {
		return fmt.Errorf("Backend registration: %w", err)
	}
	var result struct {
		SessionEpoch uint64 `json:"session_epoch"`
	}
	if err := json.Unmarshal(reply.Data, &result); err != nil || result.SessionEpoch == 0 {
		return fmt.Errorf("invalid storage registration response: %w", err)
	}
	a.epoch.Store(result.SessionEpoch)
	a.Connected.Store(true)
	defer a.Connected.Store(false)
	a.lastError.Store("")
	a.Logger.Info("HC SS registered with HomeCloudBackend", "storage_node_id", a.NodeID, "epoch", result.SessionEpoch)
	if err := a.sendInventory(ctx, conn, result.SessionEpoch); err != nil {
		return err
	}
	// Writer is protected internally by the wsclient; this goroutine sends only
	// periodic heartbeats; the main goroutine processes all incoming commands.
	liveCtx, stop := context.WithCancel(ctx)
	defer stop()
	sendErr := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		ticks := 0
		for {
			select {
			case <-liveCtx.Done():
				return
			case <-ticker.C:
				ticks++
				if ticks%3 == 0 {
					if e := a.sendInventory(liveCtx, conn, result.SessionEpoch); e != nil {
						select {
						case sendErr <- e:
						default:
						}
						_ = conn.Close()
						return
					}
				}
				hCtx, cancel := context.WithTimeout(liveCtx, 8*time.Second)
				e := send(hCtx, conn, map[string]any{"v": 1, "type": "command", "id": fmt.Sprintf("hc-ss-hb-%d", time.Now().UnixNano()), "action": "storage.agent.heartbeat", "data": map[string]any{"storage_node_id": a.NodeID, "session_epoch": result.SessionEpoch}})
				cancel()
				if e != nil {
					select {
					case sendErr <- e:
					default:
					}
					_ = conn.Close()
					return
				}
			}
		}
	}()
	for ctx.Err() == nil {
		msg, err := recv(ctx, conn)
		if err != nil {
			return err
		}
		if msg.Type == "event" && msg.Topic == "storage.agent.request" {
			a.respond(ctx, conn, msg, result.SessionEpoch)
			continue
		}
		// Responses to heartbeat, inventory and reply are informational. We never
		// mistake a successful queue ack for completion of a filesystem operation.
	}
	return ctx.Err()
}
func (a *Control) sendInventory(ctx context.Context, c *wsclient.Conn, epoch uint64) error {
	volumes, err := a.Inspector.List(ctx)
	if err != nil {
		return err
	}
	data := map[string]any{"storage_node_id": a.NodeID, "session_epoch": epoch, "volumes": volumes, "roots": a.Catalog.Published()}
    if len(a.FileWSEndpoints) > 0 {
       // endpoint/mode stay for older desktop clients; endpoints adds LAN/VPN
       // fallback without requiring network settings or credentials.
       data["file_ws"] = map[string]any{
           "endpoint": a.FileWSEndpoints[0],
           "endpoints": a.FileWSEndpoints,
           "mode": "trusted_network",
       }
    }
    packet := map[string]any{"v":1,"type":"command","id":"hc-ss-inventory","action":"storage.agent.inventory","data":data}
	return send(ctx, c, packet)
}
func strict(data json.RawMessage, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing data")
	}
	return nil
}
func (a *Control) perform(ctx context.Context, action string, data json.RawMessage) (any, error) {
	switch action {
	case "storage.agent.volumes.list":
		var arg struct{}
		if err := strict(data, &arg); err != nil {
			return nil, err
		}
		return a.Inspector.List(ctx)
	case "storage.agent.roots.list":
		var arg struct{}
		if err := strict(data, &arg); err != nil {
			return nil, err
		}
		return a.Catalog.Published(), nil
	case "storage.fs.list":
		var arg struct {
			Root   catalog.Selector `json:"root"`
			Path   string           `json:"path"`
			Limit  int              `json:"limit"`
			Cursor string           `json:"cursor"`
		}
		if err := strict(data, &arg); err != nil {
			return nil, err
		}
		if arg.Limit == 0 {
			arg.Limit = 100
		}
		result, err := a.Catalog.ListPage(arg.Root, arg.Path, arg.Limit, arg.Cursor)
		if err != nil { return nil, err }
		for i := range result.Items {
			if result.Items[i].Kind != "file" { continue }
			if id, e := sourcews.FileID(arg.Root, result.Items[i].RelativePath); e == nil {
				result.Items[i].FileID = id
			}
		}
		return result, nil
	case "storage.fs.stat":
		var arg struct {
			Root catalog.Selector `json:"root"`
			Path string           `json:"path"`
		}
		if err := strict(data, &arg); err != nil {
			return nil, err
		}
		item, err := a.Catalog.Stat(arg.Root, arg.Path)
		if err != nil { return nil, err }
		if item.Kind == "file" {
			if id, e := sourcews.FileID(arg.Root, arg.Path); e == nil {
				item.FileID = id
			}
		}
		return item, nil
	default:
		return nil, errors.New("UNKNOWN_AGENT_ACTION")
	}
}
func (a *Control) respond(ctx context.Context, c *wsclient.Conn, m message, epoch uint64) {
	var e struct {
		RequestID    string          `json:"request_id"`
		SessionEpoch uint64          `json:"session_epoch"`
		Action       string          `json:"action"`
		Data         json.RawMessage `json:"data"`
	}
	if err := strict(m.Data, &e); err != nil || e.RequestID == "" || e.SessionEpoch != epoch {
		a.Logger.Warn("invalid or stale backend request", "error", err)
		return
	}
	opCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	response, err := a.perform(opCtx, e.Action, e.Data)
	body := map[string]any{"request_id": e.RequestID, "session_epoch": epoch, "success": err == nil, "data": response}
	if err != nil {
		body["error"] = err.Error()
		body["data"] = nil
	}
	reqID := fmt.Sprintf("hc-ss-reply-%d", time.Now().UnixNano())
	if werr := send(ctx, c, map[string]any{"v": 1, "id": reqID, "type": "command", "action": "storage.agent.reply", "data": body}); werr != nil {
		a.Logger.Warn("could not deliver agent result", "error", werr)
	}
}
