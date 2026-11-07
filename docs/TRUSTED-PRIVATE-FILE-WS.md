# HC SS file data channel — private LAN (KAN-81)

The dedicated `file_ws` listener transfers file data directly between HomeCloudUI and HC Storage Service. Backend WS remains the discovery/browsing control plane; Media Gateway/SourceWS is unchanged.

## Keyless trusted private LAN (recommended for isolated home networks)

Edit the `file_ws` field **inside the existing HC SS JSON** (example IP, not the machine's actual address):

```json
{
  "file_ws": {
    "enabled": true,
    "auth_mode": "trusted_network",
    "listen": "192.168.0.147:8093",
    "allowed_client_ips": [],
    "max_upload_bytes": 1073741824
  }
}
```

- No Tailnet, bearer key or static client IP required. `allowed_client_ips: []` permits **any private IPv4 peer able to reach this listener** (RFC1918, loopback or 100.64.0.0/10).
- Optional `allowed_client_ips: ["192.168.0.25", "192.168.0.26"]` restricts access to exact peers. CIDRs and public IPs are invalid.
- Bind to an explicit **private local IPv4**, never `0.0.0.0` or a public IP. Host firewall must limit access to the intended private interface/subnet. Do not port-forward port 8093.
- Root permissions `allow_read`, `allow_write`, `allow_delete` remain enforced by HC SS; relative paths cannot escape configured roots. Browser Origin requests are rejected.

**Trust boundary:** this is network-trusted, not per-user authenticated. Any reachable private host (including a compromised IoT device) can perform whatever operations the allowed root permits. A private listener alone is insufficient if a reverse proxy/router exposes it. Plain `ws://` does not encrypt traffic against LAN observers. For an untrusted network, isolate the port, use a protected tunnel, or choose the legacy token mode. Tailscale remains optional.

## Automatic discovery

HC SS advertises the private endpoint in `storage.agent.inventory`; Backend validates it and returns it in `storage.agent.status`; HomeCloudUI discovers the endpoint for the selected node. No key or manual URL is entered in UI. The advertisement is a locator, **not authentication**. An Online HC SS agent does not by itself prove that port 8093 is reachable from the desktop.

## Token-mode compatibility

`auth_mode: "token"` still requires a 256-bit token and at least one exact allowed client IP; omitting `auth_mode` retains the old token-mode default to avoid weakening an existing deployment. HomeCloudUI's keyless automatic discovery does not use token-mode endpoints. [Legacy protocol details](KAN-81-FILE-WS-V1.md).

## Acceptance and rollback

1. Back up the live HC SS configuration and storage. Begin with a disposable folder-root; leave the full production disk read-only.
2. Update and restart HC SS and Backend; check `ss -lntp` and host firewall for the private port. Install the corresponding HomeCloudUI build.
3. Select Backend → HC SS → allowed root. Verify that operations work without manual Tailnet URL, token, or desktop IP registration.
4. In the disposable root: mkdir, upload, download with SHA-256 comparison, rename, confirmed delete, no-overwrite rejection, and interrupted-transfer cleanup.
5. Verify denied root permissions and paths, private-network reachability, and continued SourceWS/TV media playback.
6. Disable with `file_ws.enabled=false` and restart HC SS to roll back the new data channel without affecting inventory/media.

V1 does **not** yet support resume/range, recursive deletion, or directory rename. Real-device E2E is required before declaring KAN-81 complete.
