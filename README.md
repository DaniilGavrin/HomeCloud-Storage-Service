# HomeCloud Storage Service (HC SS)

**Go storage agent for physically attached disks and directories.**

Source of truth: [Confluence HC SS Architecture & WS Protocol v1](https://bytewizardru.atlassian.net/wiki/spaces/~6213e5d05d162c0069a4f9e4/pages/5275650). Related epic: [KAN-39](https://bytewizardru.atlassian.net/browse/KAN-39).

## Status (2026-11-04)

**Bootstrap / first implementation, not feature complete.** This first commit implements a working Go binary with local health endpoints, strict config parsing, automatic non-secret persistent node IDs, an in-memory catalog of explicitly published roots and read-only `List/Stat` operations. Roots have explicit `kind: disk|folder` and unique `id`; the same API works for both. No root is published by default.

**Not implemented yet:** WS control client to Backend, `/ws/source` server, HC SS registration/heartbeat, HCS1 binary frames, actual remote file reads, mutation/upload APIs, volume discovery with free/total bytes, descriptor-based filesystem security, production file IDs and versioning, remote reconnect. The `backend.control_ws_url` config is reserved and **not yet used**. `readyz` returns HTTP 503 with `ws_ready=false` until the real WS control/source functionality is implemented. Do not expose the service as a public file server.

## Quick start

```bash
go test ./...
go vet ./...
go build ./...
go run ./cmd/storage-service -config ./config.example.json
curl http://127.0.0.1:8092/healthz
curl http://127.0.0.1:8092/readyz
```

When `storage_node_id` is empty, a stable non-secret UUIDv4 is generated on first launch and stored at `<data_dir>/storage-node-id`. No private key or token is generated.

Configuration uses **JSON for the first minimal scaffold**; the Confluence YAML configuration is an illustrative target schema, not an implemented parser.

To publish a permitted disk or folder, add to the local `roots` array:

```json
{
  "id": "media-drive",
  "kind": "disk",
  "path": "/mnt/media",
  "allow_list": true,
  "allow_read": true,
  "allow_write": false,
  "allow_delete": false
}
```

or

```json
{
  "id": "photos",
  "kind": "folder",
  "path": "/mnt/media/Photos",
  "allow_list": true,
  "allow_read": true,
  "allow_write": false,
  "allow_delete": false
}
```

The root is **never** auto-enabled just because the disk is visible to Linux. The current API is only available within the Go process; an authenticated or unauthenticated remote filesystem endpoint has **not** been created yet. There are **no service keys, tokens, or pairing** planned between HomeCloud components. A private bind and filesystem allowlist remain essential given the lack of network authorization.

## Next milestones

1. Inventory, mount identities, and read-only disk/folder browser with safe descriptor-based access.
2. Control WS client: `storage.agent.hello/welcome/heartbeat`, reconnect and snapshot.
3. Private Source WS: `source.describe`, range read sessions, HCS1 and streaming backpressure.
4. Hook up the KAN-56 Gateway source resolver with the new `root: {kind,id}` selector.
