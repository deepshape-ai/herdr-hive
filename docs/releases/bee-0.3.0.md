Bee v0.3.0 shows connected Bees and their shared sessions in a new Hive tab.

1. **Honeycomb view.** Each Bee occupies a hexagonal cell with its name, connection status and sessions; this device has a honey-yellow outline, long names wrap and narrow terminals use a single column.
2. **Live directory.** Press `3` to view Hive, use Up/Down to scroll and `r` to refresh; failed refreshes mark remote entries as last known.
3. **Marketplace installation.** Install with `herdr plugin install deepshape-ai/herdr-hive/bee/plugin`; Herdr builds Bee with Go, while prebuilt release archives remain available.

The Hive tab requires Hive v0.3.0 or later and unmodified Herdr 0.9.0. The panel header now shows BEE, the device name and connection status.

Update with `bee update` or the plugin's Update action. Reopen other Bee panels to load the new interface. Configuration and share IDs are preserved; sharing briefly reconnects while local agents continue running.

[Installation and panel controls](https://github.com/deepshape-ai/herdr-hive/blob/bee/v0.3.0/bee/README.md)
