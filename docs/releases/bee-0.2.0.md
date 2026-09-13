Bee v0.2.0 can register a new device with a Hive enrollment token while saving its connection settings.

1. **Token registration.** Add `--token hreg-…` to `bee configure`, or use the masked Enrollment token field; rejected registration leaves settings unchanged and tokens are never saved to configuration.
2. **Sharing tabs.** The settings pane labels its sections `[1] Connection` and `[2] Sharing`.

Token registration requires Hive v0.2.0 with enrollment enabled. Existing registered devices remain compatible with Hive v0.1.0.

Update with the installed Bee's `bee update` command or the plugin's Update action. Configuration and share IDs are preserved; sharing briefly reconnects while local agents continue running.

[Installation and panel controls](https://github.com/deepshape-ai/herdr-hive/blob/bee/v0.2.0/bee/README.md)
