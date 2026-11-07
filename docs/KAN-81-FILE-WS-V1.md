# KAN-81 historical token-based file channel

This document describes a removed experimental configuration. The supported
workflow no longer has a `file_ws` authentication mode, token file, manual
bind or client-IP list. The shipped HC SS starts its file WebSocket
automatically on assigned private IPv4 interfaces, advertises endpoints
through Backend, and relies on host firewall/routing plus per-root filesystem
permissions. See [Automatic File Transfer](TRUSTED-PRIVATE-FILE-WS.md).

Old `file_ws` JSON fields are accepted but ignored for migration compatibility.
Do not use the instructions for generating tokens or manually enabling the
channel from historical KAN-81 builds.
