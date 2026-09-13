# Bee

Bee is a publisher-only Herdr plugin. It has four capabilities: configure Hive,
set a visible device name (hostname by default), select existing named sessions,
and switch sharing on or off. Its TUI and CLI call the same application logic.

## Install

For download, checksum and first-run commands, start with the
[Bee Quickstart](../README.md#2-install-bee-inside-herdr).

Unpack the Bee release for your OS/architecture into a permanent directory and
link that directory with Herdr:

```sh
herdr plugin link /path/to/bee-darwin-arm64 --enabled
herdr plugin pane open --plugin herdr.bee --entrypoint settings
```

The archive contains `bee` and `herdr-plugin.toml`. No Go runtime is needed. For
local development, `make package` produces the same directory under `dist/`.
The executable stays inside the plugin directory; no global `bee` command is
installed. The `bee …` examples below abbreviate `/path/to/plugin/bee …`. Adding
the directory to PATH is optional. Bee
resolves the same configuration directory used by plugin actions via
`herdr plugin config-dir herdr.bee`; `BEE_CONFIG_DIR` is an explicit test/advanced
override. No top-level `herdr bee` command is injected.

## First publication

Create a dedicated SSH device identity if you do not already have one, register
its public key with the Hive administrator, and verify Hive's host key. Encrypted
identities must be unlocked in the local SSH agent; Bee does not forward that agent.

```sh
bee configure --hive hive.example.internal:2222 \
  --identity /absolute/path/to/hive_device \
  --known-hosts /absolute/path/to/known_hosts
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

## TUI and CLI parity

| Terminal UI | CLI |
| --- | --- |
| Configure Hive | `bee configure --hive … --identity … --known-hosts …` |
| Change visible name | `bee name NAME` |
| Select or remove sessions | `bee sessions`, `bee share NAME`, `bee unshare NAME` |
| Toggle all sharing | `bee enable`, `bee disable` |
| Connection details / refresh | `bee status` |
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
