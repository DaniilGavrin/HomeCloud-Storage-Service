# KAN-81: checksum retry and Unix filesystem permissions

The existing HCS1 file data channel already computes SHA-256 during upload
and download. This update makes the reported checksum error stable:
`file.error.code = CHECKSUM_MISMATCH`. HC Storage Service never publishes
an upload with a mismatching payload checksum, and removes its temporary file.
The desktop UI retries **downloads and uploads up to three total attempts**,
starting a new WebSocket for each attempt; it does not retry permission errors,
target-exists conflicts or other non-integrity failures. A downloaded local
file is linked into place only after its size and checksum match; failed
temporary files are removed before a retry.

For newly uploaded regular files HC SS sets **0666** (`rw-rw-rw-`, no executable
bit). For directories created via HC SS it uses **0777** (`rwxrwxrwx`);
directory `x` means search/traversal, not program execution. The modes are
set explicitly after creating temporary entries to counteract restrictive
process umasks. Files and directories that existed before this change are NOT
modified, and HC SS does not perform a recursive chmod.

**Consequence:** these are intentionally world-writable Unix permissions.
Any local user/process with access to the containing directory can modify
new files. HC SS still restricts remote operations to explicitly approved
roots and root-level allow flags. Firewall rules govern network reachability
of file WS port 8093. Neither root permissions nor a firewall protect files
from malicious local accounts with filesystem access. This policy is suitable
only for storage volumes intended to be shared; system/configuration paths
must not be exported as writable HC SS roots.

Verification: upload a file, check `stat -c '%a %n' /mnt/storage/<file>`
returns `666`; create a folder through UI, check `777`; download and compare
SHA-256 on both ends. Tests inject a deliberately wrong upload hash and
verify `CHECKSUM_MISMATCH` plus absence of a published destination/temp file.
