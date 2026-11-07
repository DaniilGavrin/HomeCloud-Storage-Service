# HC SS file data channel: trusted private network

KAN-81 now supports two explicit modes on the **dedicated** file WebSocket listener. Ordinary inventory, browsing and media SourceWS remain unchanged.

- `auth_mode: "trusted_network"` (new): No additional manually copied token. Bind only an **explicit private IPv4** and allow only **exact** client IPv4 addresses. The Go service rejects requests from every other source and rejects browser `Origin` headers. Root `allow_read`, `allow_write` and `allow_delete` remain authoritative. Successful agent inventory automatically announces the endpoint to HomeCloudBackend, and HomeCloudUI discovers it from `storage.agent.status`.
- `auth_mode: "token"` (legacy/optional, default if omitted): Existing 256-bit bearer credential remains supported for users who deliberately want the additional application-layer check. A token-mode endpoint is not advertised to the keyless HomeCloudUI.

Example, **modify the existing config rather than replacing it**:

```json
{
  "file_ws": {
    "enabled": true,
    "auth_mode": "trusted_network",
    "listen": "192.168.0.147:8093",
    "allowed_client_ips": ["192.168.0.111"],
    "max_upload_bytes": 1073741824
  }
}
```

Replace both addresses with actual server and **trusted desktop** IPv4 values; `192.168.0.111` in this example might be a TV and must not be copied unless it is in fact the permitted desktop. A different approved private LAN or Tailscale IP is supported, but Tailscale is **not mandatory**.

## Threat model

This is **network-trusted, not user-authenticated**. Existing HomeCloudBackend WebSocket `hello` only identifies a client-provided `device_id`; it does not authenticate the human user. It would be unsafe to treat it as permission to delete files. The trusted-network mode intentionally replaces a bearer key with an exact source-IP boundary. Use only on a LAN which you administer, with host firewall/router ACLs preventing untrusted machines from reaching the listener. Plain `ws://` does not provide encryption or integrity against active LAN attackers. Do **not** port-forward 8093, bind `0.0.0.0`, use broad networks, or grant mutation permissions to a whole untrusted storage root.

Rollback: set `file_ws.enabled=false` and restart HC SS; browsing and media playback on the original listeners still work. For testing use a disposable folder-root, with main disks read-only, then explicitly grant write/delete only after successful tests.

## Behavior

HC SS publishes `file_ws:{"endpoint":"ws://PRIVATE_IPV4:8093/ws/files","mode":"trusted_network"}` with each inventory refresh. The Backend validates that it contains no query/credentials and is a private WS endpoint. UI uses exactly the selected agent's URL; users no longer enter a separate address or token. Data bytes do **not** traverse the Backend control WS.

No authentication is implied by this endpoint advertisement: it is a network locator, not a credential.
