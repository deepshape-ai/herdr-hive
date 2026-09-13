Bee's first release is a publisher-only Herdr plugin. Configure Hive, choose a visible name, select existing named sessions, and switch sharing on or off from the plugin settings pane.

1. **Sharing controls.** Matching visual and CLI controls preserve sharing choices and reconnect the publisher automatically.
2. **Herdr integration.** The plugin provides an Update entry and keeps its executable in the plugin directory without requiring a global `bee` command.
3. **Online upgrades.** Updates preserve configuration and share IDs, with a brief viewer disconnect during activation while local agents continue running.

Consumers use native `herdr machine add` and do not need Bee. Native compatibility is tested with Herdr 0.9.0. Sharing grants registered Hive members full control of each selected named session.

Download the archive for your OS and architecture, verify `SHA256SUMS`, then link its extracted directory with `herdr plugin link /path/to/bee --enabled`. Linux and macOS are available for amd64 and arm64; macOS binaries are not notarized.

[Installation and plugin controls](https://github.com/deepshape-ai/herdr-hive/blob/bee/v0.1.0/bee/README.md)
