Hive's first release provides a central SSH relay for sharing selected Herdr named sessions. Agents stay on their owners' machines; consumers connect with native `herdr machine add`.

1. **Share discovery.** Device keys identify members, duplicate names are resolved automatically, and each device's own shares are excluded.
2. **Relay inspection.** Inspect live connections and memory counters while bounded duplex forwarding keeps no terminal recordings.
3. **Online upgrades.** `hive update --state-dir PATH` downloads while serving, then briefly disconnects clients as the new service starts and Bee reconnects.

Native compatibility is tested with Herdr 0.9.0. Sharing grants registered members full control of the selected named session. This release does not provide zero-disconnection upgrades or automatic Herdr core upgrades.

Download the archive for your OS and architecture and verify it against `SHA256SUMS`. Linux and macOS are available for amd64 and arm64; macOS binaries are not notarized.

[Installation and operation](https://github.com/deepshape-ai/herdr-hive/blob/hive/v0.1.0/hive/README.md)
