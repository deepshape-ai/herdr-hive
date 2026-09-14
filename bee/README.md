# Bee

Bee is a publisher-only Herdr plugin. It has four capabilities: configure Hive,
set a visible device name (hostname by default), select existing named sessions,
and switch sharing on or off. Its TUI and CLI call the same application logic.

## Install

For Herdr's GitHub-managed installation, including build requirements and
reinstallation, see the [plugin installation guide](plugin/README.md):

```sh
herdr plugin install deepshape-ai/herdr-hive/bee/plugin
```

For download, checksum and first-run commands, start with the
[Bee Quickstart](../README.md#2-install-bee-inside-herdr).

Install the newest stable release and register the plugin automatically:

```sh
curl -fsSL https://raw.githubusercontent.com/deepshape-ai/herdr-hive/main/install.sh | sh -s -- bee
herdr plugin action invoke configure --plugin herdr.bee
```

The shell installer detects your OS and architecture and verifies the download.

The archive contains `bee` and `herdr-plugin.toml`. No Go runtime is needed. For
local development, `make package` produces the same directory under `dist/`.
The executable stays inside the plugin directory; no global `bee` command is
installed. The `bee …` examples below abbreviate `/path/to/plugin/bee …`. Adding
the directory to PATH is optional. Bee
resolves the same configuration directory used by plugin actions via
`herdr plugin config-dir herdr.bee`; `BEE_CONFIG_DIR` is an explicit test/advanced
override. No top-level `herdr bee` command is injected.

## First publication

1. Obtain the Hive address, an enrollment token and a verified host entry from
   the administrator. Prepare your device key and `~/.ssh/hive_known_hosts` using
   the [registration steps](../README.md#3-register-with-a-token). Bee does not
   generate the key or establish host trust automatically. If the key is encrypted,
   unlock it with `ssh-add`; Bee does not forward your SSH agent.
2. Register and save the connection settings:

   ```sh
   bee configure --hive hive.example.internal:2222 \
     --token hreg-REPLACE_WITH_YOUR_TOKEN \
     --identity "$HOME/.ssh/hive_device" \
     --known-hosts "$HOME/.ssh/hive_known_hosts"
   ```

   This requires Hive v0.2.0 or later with enrollment enabled. Registration runs
   before settings are saved; failure leaves the configuration unchanged. The
   token is never saved to `config.json`; later connections use your device key.
3. Set a name, list running sessions, then choose one and start sharing:

   ```sh
   bee name "My workstation"
   bee sessions
   bee share project
   bee enable
   bee status
   ```

`project` must be an existing, running **Herdr named session**, not a workspace or
agent name. Share selection is saved while sharing is off. `enable` launches one
local background publisher; closing its TUI does not stop sharing. A plugin
startup hook restores sharing only when the saved global switch is enabled.
The background publisher is not an OS login service and is not installed system-wide.

Commands return JSON; errors have nonzero exit codes. An `enable` response with
`connected: true` and share IDs confirms registration. A failed initial connection
is explicitly reported as pending with a nonzero exit code; the enabled publisher
continues retrying. Use `status` to distinguish saved intent from live connectivity.

`status` includes the name resolved by Hive. Display names can change without
changing share IDs. One device identity should belong to one Bee instance; a
second concurrent publisher using that identity is rejected.

The Connection panel also accepts a masked enrollment token and clears it after
saving. It uses `~/.ssh/known_hosts` by default, or the custom file already saved
by `configure`. You can omit the token when the device key is already authorized.

## Panel controls

Bee opens beside the current pane. Open the action again to focus the existing
Bee pane in this tab; invoke it from that pane to close it. Closing the UI leaves
sharing running. Add this binding to Herdr's `config.toml`, then run
`herdr server reload-config`:

```toml
[[keys.command]]
key = "prefix+alt+b"
type = "plugin_action"
command = "herdr.bee.configure"
description = "Open Bee"
```

| Input | Action |
| --- | --- |
| `1` / `2`, Left / Right | Connection / Sharing |
| Tab / Shift+Tab, Up / Down | Move between fields or sessions |
| Enter, click | Edit a field or activate a control |
| Space, click a session | Share or unshare that session |
| Ctrl+S | Save edited connection fields |
| `s` | Start or pause sharing |
| `u` | Update Bee |
| `r` | Refresh now |
| `q` / Escape | Close the pane (Escape first leaves an edited field) |

Text fields support normal editing and bracketed paste. Shortcuts such as `s`
and `q` remain text while editing. The UI refreshes after actions and every two
seconds when idle, with one snapshot request in flight. Slow responses do not
block input; an error is shown without discarding drafts. Untouched fields pick
up CLI changes while edited fields stay local until saved. Small panes scroll
focused controls into view, and colors follow the terminal's light/dark theme.

Host verification uses `~/.ssh/known_hosts` by default. Existing custom trust-store
paths are preserved; `bee configure --known-hosts PATH` remains available as a CLI
override. The standard panel does not expose this SSH setting.

The UI uses Bubble Tea's state-driven model and Lip Gloss styling, compiled into
the existing Go binary. No browser, Node runtime or local web server is required.

## TUI and CLI parity

| Terminal UI | CLI |
| --- | --- |
| Connection: Hive and SSH fields | `bee configure --hive … --identity … --known-hosts …` |
| Connection: enrollment token | `bee configure --hive … --identity … --token hreg-…` |
| Connection: visible name | `bee name NAME` |
| Sharing: select or remove | `bee sessions`, `bee share NAME`, `bee unshare NAME` |
| Start / pause sharing | `bee enable`, `bee disable` |
| Live connection status / refresh | `bee status` |
| Update Bee | `bee update` |

The Herdr actions `configure`, `enable` and `disable` are fixed entrypoints:

```sh
herdr plugin action invoke configure --plugin herdr.bee
herdr plugin action invoke enable --plugin herdr.bee
herdr plugin action invoke disable --plugin herdr.bee
```

Herdr action invocation is asynchronous: its command log alone is not a publication
receipt. Use the bundled `bee` CLI when an agent needs parameters and completion
results. Consumer operations remain native Herdr commands.

## Update

```sh
# Use the Update entry in the plugin settings pane, or invoke it through Herdr:
herdr plugin action invoke update --plugin herdr.bee
# For synchronous results, run the bundled executable without adding it to PATH:
/path/to/bee-darwin-arm64/bee update
```

This fetches the newest stable **Bee** release from this repository, verifies the
archive checksum and binary version, and replaces the executable in its existing
installation directory. A linked plugin's manifest is updated alongside it. No
GitHub token, Go installation or additional updater service is required. The
installation directory must be writable by the user running the command.

The running publisher switches automatically to the new executable and retains
its configuration and share IDs. Downloads leave current sharing online; process
replacement briefly disconnects viewers and re-registers publication. Local agents
continue running. The plugin settings pane reopens with the new code after an
update; other already-open settings panes must be reopened. Herdr may cache plugin
manifest metadata until its next reload. CLI results include the installed version
and, when a running publisher was switched, its actual status. A pending network
reconnect is distinct from a successful executable switch.

Updates are explicitly triggered and have a three-minute download deadline. There
is no periodic download, silent upgrade or version rollback command. Failed
checksums and download errors leave the old executable and publisher running.
See [upgrade behavior](../docs/UPDATING.md) for limits and failure handling.

## Stop or uninstall

```sh
bee disable
herdr plugin unlink herdr.bee
```

Then remove the unpacked plugin directory if desired. Disabling sharing preserves
settings, closes active remote attachments and leaves local sessions running.
The publisher exits after acknowledging the disable operation. Removing a share
and sharing it again generates a new ID; turning the global switch off/on retains
IDs. Renaming a session requires selecting its new name.

## Configuration and lifecycle

`config.json` is versioned and atomically updated under a file lock. It stores
only SSH file references, Hive address, visible name, manual rules and the global
switch. Private key contents are never copied into config. Unknown versions,
fields and rule types fail closed without overwriting the file. Future matching
rules can extend the rule discriminator without changing manual rule semantics.

An enabled rule refers to the selected named session, including its future panes
and agents. It is not tied to a particular agent process. A running connection
checks socket identity before opening streams. Herdr session restart/replacement
requires a publication refresh (`bee enable`) if the old socket binding remains
online. No automatic agent restart, prompt replay or resume is performed.

Bee needs both Herdr socket files and local `herdr status` access. An unavailable
selected session currently keeps publication pending until it is available or
removed from selection. Configuration changes restart the publication transport
and disconnect viewers. This simple first-version behavior avoids partial stale
publication state.
