# HomeCloud HC SS file.move (KAN-81)

Move a regular file between directories in the **same published root and
filesystem**, without streaming the bytes through HomeCloudUI or Backend.

Client WebSocket request (same `/ws/files` listener as uploads):
```json
{"v":1,"id":"move-1","type":"file.move",
 "root":{"kind":"disk","id":"storage-hdd"},
 "path":"Глянцевый логотип HomeCloud TV Player.png",
 "new_path":"Movie/Глянцевый логотип HomeCloud TV Player.png"}
```
The server replies with `file.complete` when the file is moved, or
`file.error` with a code such as `TARGET_EXISTS`, `NOT_FOUND`,
`INVALID_PATH`, `WRITE_FORBIDDEN` or `FILE_OPERATION_FAILED`.

The action requires **allow_write**, not **allow_delete**: moving preserves
the file, and should be available on writable roots even if deletion is
disabled. A move is implemented by a root-confined hard link followed by
unlinking the old name; there is **no overwrite** and no transfer of file
bytes. If removal fails after the new link is created, the new link is
removed as a rollback attempt. Directories cannot be moved in v1.

The directory chooser in HomeCloudUI lists existing directories through the
Backend control WS, and the file.move request goes directly to HC SS.
The source/target are validated relative to the same permitted root.

Limitation: hard links cannot cross filesystem/mount boundaries. Those moves
must be handled separately later via a verified copy/move workflow; **do not
silently fall back to unverified copy-then-delete**.
