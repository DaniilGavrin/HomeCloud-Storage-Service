# KAN-73 — First HomeCloud Storage Service run on the real Ubuntu storage host

**Prerequisites:** Both Backend and HC SS KAN-73 patches are installed, tested and committed. The running Backend process must have been restarted with its newly built code. The storage server must be able to contact its Backend private/Tailscale WS address. Backend must accept inbound WS on that private interface (not loopback-only).

## 1. Read-only preflight on Ubuntu storage host

```bash
uname -m
lsblk -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS
findmnt -T /path/to/desired/MEDIA_DIRECTORY
ip -brief addr
tailscale status
```

Write down the **exact** mounted data directory. Do not assume `/mnt/data` or a user's home directory. Do not publish the system disk. Check whether the service account can traverse the directory.

## 2. Get the Linux binary

After GitHub Actions succeeds, download the `hc-storage-service-linux-amd64` binary from the uploaded Actions artifact, OR cross-compile it on your Windows workstation:

```powershell
cd C:\Users\user\IdeaProjects\HomeCloud-Storage-Service
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
go build -trimpath -o "$env:TEMP\hc-storage-service-linux-amd64" ./cmd/storage-service
Remove-Item Env:GOOS,Env:GOARCH,Env:CGO_ENABLED
scp "$env:TEMP\hc-storage-service-linux-amd64" YOUR_LINUX_USER@YOUR_SERVER_IP:~/hc-storage-service
```

The `scp` command is an example; your existing NFS transfer path is also acceptable. Never SSH into an unintended address or overwrite a different service binary.

## 3. User-level smoke run on Ubuntu (before systemd)

```bash
mkdir -p "$HOME/homecloud-ss/data"
mv ~/hc-storage-service "$HOME/homecloud-ss/hc-storage-service"
chmod 755 "$HOME/homecloud-ss/hc-storage-service"
cat > "$HOME/homecloud-ss/config.json" <<'JSON'
{
  "config_version": 1,
  "storage_node_id": "",
  "data_dir": "./data",
  "http_listen": "127.0.0.1:8092",
  "backend": { "control_ws_url": "ws://BACKEND_PRIVATE_OR_TAILSCALE_IP:8080/ws" },
  "roots": [
    {
      "id": "movies",
      "kind": "folder",
      "path": "/ACTUAL/MOUNTED/DATA/FOLDER",
      "allow_list": true,
      "allow_read": true,
      "allow_write": false,
      "allow_delete": false
    }
  ]
}
JSON
# EDIT BOTH PLACEHOLDERS in config.json before starting.
cd "$HOME/homecloud-ss"
./hc-storage-service -config ./config.json
```

Use another terminal for:

```bash
curl -i http://127.0.0.1:8092/healthz
curl -i http://127.0.0.1:8092/readyz
```

Expected: `healthz` HTTP 200 immediately; `readyz` HTTP 200 and `ws_ready:true` **only after Backend accepted storage.agent.register**. If Backend is down/unreachable, `readyz` stays 503 and the agent logs a reconnection error; this is expected. `storage_node_id` is generated once and persisted under `data/storage-node-id`.

## 4. End-to-end from Windows workstation

Use the actual Backend WS URL running on your workstation, and execute after the Linux agent reports registration:

```powershell
cd C:\Users\user\IdeaProjects\HomeCloud-Storage-Service
go run ./cmd/hcss-smoke -backend ws://127.0.0.1:8080/ws -root-kind folder -root-id movies
```

This reads `storage.agent.status`, `storage.agent.volumes.list`, `storage.agent.roots.list`, `storage.fs.list`, and `storage.fs.stat` through **the actual Backend → HC SS** route. Run once with the root and once without if you have not configured one yet. Validate physical disk inventory against `lsblk` and the target file list against `ls -la` on the server.

## 5. Operational checks

1. Stop the HC SS process with Ctrl+C; `/readyz` becomes unavailable and the Backend agent should disappear from the online registry.
2. Restart; `storage_node_id` must remain unchanged, `boot_id` changes, new `session_epoch` is assigned.
3. Temporarily disconnect Backend, then restore it; HC SS reconnects with backoff and republishes inventory.
4. Test `kind:disk` only after verifying that the configured absolute path is exactly the intended mounted volume root. Leave `allow_write` and `allow_delete` false.
5. Test `../outside`, absolute/Windows paths and mismatching `kind` — all must be rejected.

## Security boundary

No keys, tokens, passwords, service certificates or other service authorization are involved. The WS Backend and agent interfaces must not be exposed to the Internet. Plain `ws://` is not encrypted; restrict access using LAN isolation, private/Tailscale interfaces and host firewall. Anyone who can reach Backend control WS may be able to request reads of **published** roots. The agent does not support network root creation or mutation in this stage.

## Limits

This is **not** a complete Source WS/HCS1 server and does not stream movie bytes to Gateway. Local path resolution remains basic, read-only and not yet TOCTOU-resistant under hostile concurrent modifications. No on-server tests were run by ChatGPT; this document is a reproducible test plan for the user to execute on their host.
