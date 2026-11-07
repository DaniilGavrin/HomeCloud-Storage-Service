# HomeCloud HC SS — automatic file transfer (KAN-81)

The HC Storage Service **always** starts its separate WebSocket data plane on
port **8093**. No `file_ws.enabled`, bind address, token, login, device-IP ACL
or `auth_mode` is configured. The original JSON keys are silently ignored
when upgrading an existing installation; they may be removed later.

At startup, HC SS discovers assigned **private IPv4** addresses (RFC1918 and
100.64.0.0/10), binds a listener on each and publishes the successful addresses
through `storage.agent.inventory.file_ws`. LAN addresses are ordered before
overlay addresses. Backend validates/forwards the addresses; HomeCloudUI tries
the available endpoints in order with connection fallback. The metadata-only
Backend WebSocket stays separate from the file WebSocket.

Discovery example (not a fixed installation address):

```json
{"file_ws":{
  "endpoint":"ws://192.168.0.147:8093/ws/files",
  "endpoints":[
    "ws://192.168.0.147:8093/ws/files",
    "ws://100.114.58.74:8093/ws/files"
  ],
  "mode":"trusted_network"
}}
```

`endpoint` and `mode` remain only as compatibility fields for older
HomeCloudUI versions. Neither field changes server access policy.

**Network boundary:** The host firewall, network segmentation and routing
control which clients can reach port 8093. Do not forward it to the public
Internet. No end-user authentication is implemented on this channel, and plain
`ws://` has no transport encryption of its own. Treat every host allowed by
the firewall as able to exercise the permissions of explicitly published
storage roots. Protect port 8093 accordingly; use an encrypted tunnel where
necessary. HC SS still enforces `allow_read`, `allow_write`, `allow_delete`
and descriptor-rooted path containment.

`/healthz` and `/readyz` stay on the existing `http_listen` (usually
127.0.0.1:8092). Source WS / HCS1 to Media Gateway is unchanged.

Upgrade order: **Backend → HC SS → HomeCloudUI**. Once updated, restart
HC SS and verify its logs for "HC SS automatic file transfer enabled",
`ss -lntp | grep :8093`, and the UI's automatically discovered file channel.
A private interface must be present when the agent starts to serve remote
clients. Restart HC SS after changing the machine's network interfaces.

Rollback: restore the prior binaries and configuration backup together;
do not delete any storage volumes or directories.
