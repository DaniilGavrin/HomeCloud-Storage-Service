# HomeCloud Storage Service (HC SS)

**Go storage agent for physically attached disks and directories.**

Source of truth: [Confluence HC SS Architecture & WS Protocol v1](https://bytewizardru.atlassian.net/wiki/spaces/~6213e5d05d162c0069a4f9e4/pages/5275650). Related epic: [KAN-39](https://bytewizardru.atlassian.net/browse/KAN-39).

## Status (2026-11-04) — KAN-73 local implementation

**Working:** independently launched Go service; configuration; stable non-secret node ID; read-only disk/folder root catalog; real mounted-volume/physical block-device inventory; page-based list/stat; dedicated outgoing WebSocket control client with RFC6455, standard Backend hello/identified handshake, `storage.agent.register`, inventory updates every 60s, heartbeat every 20s, reconnect and generation fencing. Backend patch is required for new commands. `readyz` reports 200 only while registered with Backend.

**Not yet implemented:** `/ws/source` and HCS1 binary data transfer to Media Gateway; durable file IDs; upload/rename/delete; secure descriptor-based filesystem operations under concurrent path replacement; automatic systemd deployment. All shared roots must be explicitly configured locally. No key/token/certificate/password is used to authenticate Storage Service to Backend/Gateway. WebSocket's standard frame masks/handshake nonce are RFC6455 framing details and are not authorization keys.

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

## KAN-73 — Backend control WS + disk inventory (next stage)

- Automatically discovers mounted volumes (Linux `/proc/self/mountinfo` + `statfs`; Windows logical drive capacities) without granting any file access.
- Connects to an existing HomeCloudBackend control WebSocket, using the **existing** initial protocol `hello` → `identified`, then `storage.agent.register` → `result`. The Backend companion patch in the KAN-73 archive is **required** for `storage.agent.register`.
- Registers non-secret `storage_node_id`, persistent across launches. No API keys, tokens or passwords. Periodic `storage.agent.heartbeat` and reconnection. Sends an inventory snapshot for visible disk volumes and locally approved disk/folder roots.
- Receives Backend `storage.agent.request` events and replies through `storage.agent.reply` to handle `storage.agent.volumes.list`, `storage.agent.roots.list`, `storage.fs.list` (with best-effort cursor pagination), and `storage.fs.stat`. Filesystem operations remain **read-only**, with root selector `{kind:"disk"|"folder",id}` and paths relative to that root.
- `/readyz` returns 200 **only while the Backend WS is connected and registered**; `/healthz` only asserts that the process is alive.

**Not part of KAN-73:** Source WS/HCS1 binary reads, actual media delivery, file mutation, configurable remote root allowlist updates, secure descriptor-based path operations under concurrent filesystem mutation. The catalog path check is still not a production-grade TOCTOU-resistant reader. Do not expose this agent's control or diagnostics to the public Internet.

### Production config example (adjust IPs and paths)

```json
{
  "config_version": 1,
  "storage_node_id": "",
  "data_dir": "/var/lib/homecloud-storage",
  "http_listen": "127.0.0.1:8092",
  "backend": {"control_ws_url": "ws://100.82.22.45:8080/ws"},
  "roots": [
    {"id":"movies","kind":"folder","path":"/mnt/data/Movies","allow_list":true,"allow_read":true,"allow_write":false,"allow_delete":false}
  ]
}
```

The Tailscale/LAN route must be reachable by the storage host. The path shown is an **example**; choose the actual mountpoint after inspecting `findmnt -T <path>` and permissions. A root with `kind=disk` should point to the correct mounted filesystem root. Never publish the system disk just because it was discovered.

### End-to-end smoke after installing both backend and agent patch

```powershell
cd C:\Users\user\IdeaProjects\HomeCloud-Storage-Service
go run ./cmd/hcss-smoke -backend ws://127.0.0.1:8080/ws
# optionally browse your configured folder after reviewing its published root ID:
go run ./cmd/hcss-smoke -backend ws://127.0.0.1:8080/ws -root-kind folder -root-id movies -path ""
```

Linux production deployment: see `docs/KAN-73-DEPLOY-UBUNTU.md`. This testing tool only uses the existing (unauthenticated) private Backend control WebSocket.
