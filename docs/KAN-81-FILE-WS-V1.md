# KAN-81 — HC SS authenticated file WebSocket v1

## Scope

An explicitly enabled native data plane on the **storage node**. The identified Backend WebSocket remains responsible for `storage.agent.status`, `storage.fs.list`, media rooms and other control/metadata requests. **No file bytes or admin credentials traverse Backend WS or Media Gateway.**

This is not authorization based on `client_type: desktop`: as of KAN-81 the Backend control WS `hello` is not a user-authenticated identity. Filesystem modification over that channel would be unsafe. KAN-81 instead requires **a separate 256-bit per-node token AND an exact Tailscale source-IP ACL**. Neither the transfer listener nor its token is advertised by Backend.

## Configuration (explicit opt-in)

Configure `file_ws` in HC SS JSON:

```json
{
  "enabled": true,
  "listen": "100.101.102.103:8093",
  "allowed_client_ips": ["100.107.108.109"],
  "token_file": "/root/homecloud/data/filews-token",
  "max_upload_bytes": 34359738368
}
```

This object is **the `file_ws` field** inside the existing `config_version: 1` configuration, not a separate config file. Replace both sample addresses with the **real** Tailscale IPv4 of the HC SS host and the actual trusted client device. Only `100.64.0.0/10` and loopback can bind/connect. The service refuses untrusted client IPs, browser `Origin` headers, missing or incorrect keys. Exact source IP comes from the TCP socket; `X-Forwarded-For` is ignored.

On first start, HC SS generates a random token in `token_file` with mode 0600, inside a 0700 data directory when newly created. To retrieve it **locally as the server owner**, read it via an authenticated SSH session, for example:

```bash
sudo cat /root/homecloud/data/filews-token
```

Never paste the token into Jira, GitHub, logs, chat, shell history or screenshots; enter it in HomeCloudUI's ephemeral password field. The token is not persisted by UI. If exposed, stop HC SS, remove/rotate the token file after securing access, and restart. Existing files are not touched by token rotation.

A Tailnet connection uses WireGuard encryption. **Do not expose port 8093 to public WAN, general LAN, a reverse proxy or TLS-stripping middlebox.** Native UI rejects non-Tailnet remote addresses. Firewall and Tailscale ACLs should also restrict the selected client to this port. The original `http_listen` for health/source WS stays separate (e.g. loopback 8092).

## One connection, one operation

Endpoint: `ws://<HC_SS_TAILNET_IP>:8093/ws/files`, `Authorization: Bearer <64 lowercase hex characters>`, HTTP Upgrade to WS. No token in URI or query params.

Initial JSON:

```json
{"v":1,"id":"file-unique","type":"file.get","root":{"kind":"disk","id":"storage-hdd"},"path":"dir/file.mkv"}
```

Types: `file.get`, `file.put` (requires `size_bytes`), `file.mkdir`, `file.rename` (requires `new_path`; regular files only), `file.delete` (files or **empty** dirs only). All paths are root-relative, no `..`, colon, leading slash, backslash or implicit root selection. File permissions are enforced **on the HC SS node** from `catalog.Root`: `allow_read`, `allow_write`, `allow_delete`. This is independent of any client UI.

- GET → `file.ready {size_bytes}`, 128-KiB-max binary frames, `file.complete {size_bytes,sha256}`. UI writes an adjacent temporary file, verifies bytes/hash, fsyncs and publishes without replacing an existing local file.
- PUT → `file.ready {size_bytes}`, binary frames, client `file.end {sha256}`, server `file.complete {size_bytes,sha256}`. Service uses `os.Root` to stay inside the whitelisted directory, exclusive random temporary files, bounded memory, fsync then hard-link publication. **Never overwrite** existing files. Partial transfer files are cleaned after failure.
- Metadata operations reply with `file.complete` or `file.error {code}` (no host filesystem paths).
- At most 2 concurrent connections by default, 40 s per read or 25 s per write, default upload limit 32 GiB, hard maximum config 1 TiB.

Failures never fall back to the old Backend Storage Manager transfer endpoints. `file.rename` deliberately does **not** support directory moves yet; recursive deletion is unavailable. Filesystem boundaries under an allowed root can still include nested mounts; do not grant permissions on an overly broad root.

## Deployment order / rollback

1. Back up HC SS config and the selected root data. The server process must have read/write OS permissions to the admin-approved roots.
2. Update HC SS binary (verify SHA256), preserve its node ID/config, add the opt-in `file_ws` config, restrict Tailnet ACL/firewall.
3. Confirm `/readyz` and Backend registration on the old local port, plus new `ss -lntp | grep 8093`.
4. Test small upload (new filename), download, SHA-256, mkdir, file rename and explicit delete in **a disposable test directory**, then interruption/restart and denied permissions.
5. Install HomeCloudUI from the corresponding KAN-81 PR/release and provide the Tailnet endpoint and admin token only through UI. Check that switching HC SS nodes clears the token.
6. For rollback stop HC SS, revert binary/config to the backed-up version and restart. No migration of filesystem structure or DB is performed.

## Deferred hardening

- Authenticated Backend user/device identities, ideally short-lived delegated transfer tickets instead of a static user-entered token.
- Durable resumable uploads and range downloads, per-transfer quotas and audit metadata, version conflict resolution, and proper directory-rename (no-overwrite).
- HCSS list pagination cursor pass-through in Backend, separately scoped.
- HomeCloudUI headless integration tests against a disposable HC SS node; green CI does not claim deployment success.
