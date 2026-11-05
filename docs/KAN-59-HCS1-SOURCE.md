# KAN-59 / HCS1 — Storage Service binary source, draft v1

This is a read-only, PRIVATE service-to-service WebSocket. It does not
implement NFS, SMB, a cloud proxy or filesystem upload/mutation.

## Enrollment and security boundary

Storage Service's existing Backend control registration does not authenticate
the sending peer cryptographically. Likewise `storage_node_id` and
`file_id` are routing labels, NOT passwords. Do not publish the service on
the internet or allow untrusted LAN clients. The /ws/source endpoint is OFF
by default. To enable, bind `http_listen` to one explicit private IP (never
0.0.0.0) and list exact Gateway source IP addresses. Tailscale node IPs are
allowed and preferable for cross-host traffic because Tailscale encrypts
the transport. The IP allowlist restricts requests but is not a substitute
for cryptographic service identity or mutual authentication.

Example HCSS config snippet on the disk server:

```json
{
  "config_version": 1,
  "storage_node_id": "",
  "data_dir": "/var/lib/homecloud-storage",
  "http_listen": "192.168.0.10:8092",
  "source_ws": {
    "enabled": true,
    "allowed_gateway_ips": ["192.168.0.20"]
  },
  "backend": {"control_ws_url": "ws://192.168.0.20:8080/ws"},
  "roots": [
    {"id":"media","kind":"folder","path":"/mnt/data/Media",
     "allow_list":true,"allow_read":true,
     "allow_write":false,"allow_delete":false}
  ]
}
```

All IP addresses and mount paths above are examples. The Gateway machine
must have a route to `http_listen`, and the allowed_gateway_ips value must
match its SOURCE IP as observed by Storage Service. Loopback setups instead
use 127.0.0.1 for both bind and allowlist.

## File identifiers

`storage.fs.list` and `storage.fs.stat` return `file_id` for regular
files under an approved root. IDs are deterministic across restarts and
encode only `root:{kind,id}` plus a relative path; they do not contain
the host filesystem's absolute mount path. They are NOT secret tokens.
The source endpoint independently reopens every requested file under its
approved root using `os.OpenRoot` to reject symlink traversal.

## HCS1 messages

Private `ws://<HCSS-IP>:8092/ws/source`, one operation at a time per
connection. Gateway sends JSON text:

```json
{"v":1,"type":"source.stat","id":"req-1","file_id":"hc1.<encoded>"}
```

Response:

```json
{"v":1,"type":"source.metadata","id":"req-1",
 "metadata":{"owner_node_id":"storage-node-id","file_id":"hc1.<encoded>",
  "version":"sha256-hex","size_bytes":123456,"content_type":"audio/mpeg",
  "range_readable":true,
  "local_hint":{"shared_root_id":"folder:media","relative_path":"music/track.mp3","modified_unix_nano":123456789}}}
```

The range request (bytes, NOT milliseconds):

```json
{"v":1,"type":"source.read","id":"req-2","file_id":"hc1.<encoded>",
 "version":"sha256-hex","offset":0,"length":262144}
```

The server responds with a JSON `source.range` header containing
`id,offset,length,version,eof,checksum_sha256`, immediately followed by
exactly ONE WebSocket binary message containing exactly `length` raw file
bytes. The range must be between 1 and 4,194,304 bytes. At EOF the binary
message may be empty, but the header always follows the request.

Failures are `{"v":1,"type":"source.error","id":"req-2","code":"..."}`;
there is no binary message after an error. `SOURCE_VERSION_CHANGED` prevents
mixing versions; other codes include `INVALID_FILE_ID`, `INVALID_RANGE`,
`PATH_NOT_ALLOWED` and `SOURCE_UNAVAILABLE`.

The SHA-256 checksum protects transfer integrity. A version derived from
size/mtime/path does NOT cryptographically prove unchanged content against an
adversarial same-size/same-mtime replacement; stronger content revision /
inode pinning remains future hardening.

## Status / limits

This PR implements HCS1 read-only metadata and binary range messages, tests
on real temporary files and CI. It does NOT implement pairing, security
credentials, remote write/delete, realtime changes, FFmpeg, Media3, or TV
decoding. For live video/audio the Media Gateway must be configured and an
actual player must consume the output.

`go test -race -count=1 ./... && go vet ./... && go build ./...`.
