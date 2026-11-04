// hcss-smoke calls a real HomeCloud Backend WS to verify Storage Service v1.
// It never mutates files or changes root permissions. Requires HC SS + Backend.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/wsclient"
	"log"
	"os"
	"time"
)

func main() {
	backend := flag.String("backend", "ws://127.0.0.1:8080/ws", "Backend control WebSocket")
	node := flag.String("node", "", "storage_node_id; leave empty to use first online node")
	rootKind := flag.String("root-kind", "", "disk or folder; optional")
	rootID := flag.String("root-id", "", "published root ID; optional")
	folder := flag.String("path", "", "relative path inside root")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := wsclient.Dial(ctx, *backend)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	send := func(v any) {
		b, e := json.Marshal(v)
		if e != nil {
			log.Fatal(e)
		}
		if e = c.WriteText(ctx, b); e != nil {
			log.Fatal(e)
		}
	}
	read := func(want string) map[string]any {
		for {
			b, e := c.ReadText(ctx)
			if e != nil {
				log.Fatal(e)
			}
			var a map[string]any
			if e = json.Unmarshal(b, &a); e != nil {
				log.Fatal(e)
			}
			if a["type"] == "event" {
				continue
			}
			if a["id"] != want {
				log.Fatalf("response ID mismatch, got %v want %v", a["id"], want)
			}
			if a["type"] == "error" {
				log.Fatalf("Backend response error: %v", a["error"])
			}
			return a
		}
	}
	send(map[string]any{"v": 1, "id": "smoke-hello", "type": "hello", "device_id": "storage-smoke-cli", "client_type": "cli"})
	if x := read("smoke-hello"); x["type"] != "identified" {
		log.Fatalf("unexpected hello response %v", x)
	}
	call := func(id, action string, data any) map[string]any {
		send(map[string]any{"v": 1, "id": id, "type": "command", "action": action, "data": data})
		reply := read(id)
		if reply["type"] != "result" {
			log.Fatalf("unexpected reply %v", reply)
		}
		payload, _ := json.MarshalIndent(reply["data"], "", "  ")
		fmt.Println("# " + action + "\n" + string(payload))
		var v map[string]any
		if a, ok := reply["data"].(map[string]any); ok {
			v = a
		}
		return v
	}
	status := call("s-1", "storage.agent.status", map[string]any{})
	if *node == "" {
		if list, ok := status["agents"].([]any); ok {
			for _, x := range list {
				a, ok := x.(map[string]any)
				if ok && a["online"] == true {
					*node, _ = a["storage_node_id"].(string)
					break
				}
			}
		}
	}
	if *node == "" {
		log.Fatal("no ONLINE HomeCloud Storage Service in Backend registry")
	}
	id := *node
	fmt.Fprintln(os.Stderr, "Selected storage node:", id)
	call("s-2", "storage.agent.volumes.list", map[string]any{"storage_node_id": id})
	call("s-3", "storage.agent.roots.list", map[string]any{"storage_node_id": id})
	if *rootKind != "" && *rootID != "" {
		root := map[string]any{"kind": *rootKind, "id": *rootID}
		call("s-4", "storage.fs.list", map[string]any{"storage_node_id": id, "root": root, "path": *folder, "limit": 100, "cursor": ""})
		call("s-5", "storage.fs.stat", map[string]any{"storage_node_id": id, "root": root, "path": *folder})
	}
	fmt.Println("[PASS] HC SS Backend WS smoke completed")
}
