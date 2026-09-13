Bee v0.1.1 replaces the text menu with a dedicated settings pane for connection details and session sharing.

1. **Settings pane.** Connection and Sessions tabs support keyboard and mouse input, light and dark themes, and scrolling in small panes.
2. **Live status.** Connection status and CLI changes refresh automatically while unsaved field edits stay intact.
3. **Pane controls.** The configure action opens, focuses or closes Bee in the current tab; it can be bound to `prefix+alt+b`, and closing the pane keeps sharing running.

Install or update to the latest stable Bee release:

```sh
curl -fsSL https://raw.githubusercontent.com/deepshape-ai/herdr-hive/main/install.sh | sh -s -- bee
```

Existing installations can also use the plugin's Update action. Configuration and share IDs are preserved; activating an update briefly reconnects sharing while local agents continue running. Hive v0.1.0 remains compatible.

[Installation and panel controls](https://github.com/deepshape-ai/herdr-hive/blob/bee/v0.1.1/bee/README.md)
