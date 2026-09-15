# Bee

Bee publishes selected Herdr sessions and shows who is sharing in the same Hive.
Configure Hive, set a visible device name (hostname by default), select existing
named sessions, and switch sharing on or off. Its TUI and CLI call the same application logic.

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

1. Obtain a `hinv1-` invitation from the administrator. In Bee's Connection
   panel, paste it into Invitation and click Join Hive. Bee generates a dedicated
   device key, pins the provided Hive host key, enrolls, and adds the native Hive
   machine. Joining leaves sharing off on a new device.
2. For scripts, pass the invitation on standard input, outside process arguments:

   ```sh
   bee join --stdin < /secure/path/invitation.txt
   ```

   The input is the invitation value alone, not the administrator's entire JSON
   response. After successful registration, `bee join` without flags resumes or
   repairs native connection setup using the saved identity, even if the invitation
   has expired. The invitation/token is never saved. Manual `configure` remains
   available under Advanced settings; see the [manual path](../README.md#manual-registration-advanced).
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

Commands return JSON, except `bee on`, which preserves native Herdr output and
exit status; errors have nonzero exit codes. An `enable` response with
`connected: true` and share IDs confirms registration. A failed initial connection
is explicitly reported as pending with a nonzero exit code; the enabled publisher
continues retrying. Use `status` to distinguish saved intent from live connectivity.

`status` includes the name resolved by Hive. Display names can change without
changing share IDs. One device identity should belong to one Bee instance; a
second concurrent publisher using that identity is rejected.

The Advanced settings panel also accepts a masked enrollment token and clears it after
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
| `1` / `2` / `3`, Left / Right | Connection / Sharing / Hive |
| Tab / Shift+Tab, Up / Down | Move between controls; scroll the Hive list |
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

The **Hive** tab arranges Bees as a honeycomb, with this device marked `YOU`
and outlined in honey yellow. Each cell shows the Bee name, connection status
and published sessions. The layout adapts from one to three columns; long names
wrap inside cells, and larger hives scroll vertically. It queries Hive with the configured device identity and verified
host key, then adds this device's live publisher status because Hive's directory
excludes the requesting device. Only online publications are listed; registered
but disconnected devices and unshared sessions do not appear. The list refreshes
asynchronously about every five seconds while the tab is open. Failed refreshes keep
remote entries explicitly marked as last known until a query succeeds. Press `r`
to retry, or use Up/Down and Page Up/Page Down to scroll larger lists.

This tab is a directory. To open remote sessions in Herdr, complete
[the receiving setup](../README.md#5-view-other-members-sharing).

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

## Remote agent automation

Every enrolled Bee can consume other devices' published sessions, including while
its own sharing is off. Upgrade Hive and the publishing Bee to a version with API
support, then use the existing identity and host trust; no host SSH login, new
service, transport credential, or Herdr modification is required.

1. Discover online targets:

   ```sh
   bee targets
   ```

   Each record includes `id`, `name`, `label` (Herdr session), `api`, and
   `generation`. Select a stable share `id`, or the exact `name/label` pair.
   Ambiguous names are rejected. Your own publications are excluded; use local
   Herdr commands for those. A missing `api: true` or generation means that Hive
   or the publishing Bee needs an upgrade.

2. Run the native Herdr CLI in that target's context. These examples assume a
   Bee named `B` sharing session `research`:

   ```sh
   bee on B/research -- herdr workspace list
   bee on B/research -- herdr agent list
   bee on B/research -- herdr agent prompt reviewer "Inspect the current changes" --wait --timeout 120000
   bee on B/research -- herdr agent read reviewer --source recent-unwrapped --lines 120
   ```

3. To create an agent, first create its terminal and use the returned ID:

   ```sh
   created=$(bee on B/research -- herdr workspace create --cwd /srv/project --label review --no-focus)
   pane=$(printf '%s\n' "$created" | jq -r '.result.root_pane.pane_id')
   bee on B/research -- herdr agent start reviewer --kind codex --pane "$pane"
   ```

   `/srv/project` is a directory on B. The agent executable and its account must
   already be configured on B. Startup success means native Herdr detected an
   interactive agent. If startup blocks or fails, inspect the returned name/pane;
   do not blindly repeat creation. A created pane is not automatically deleted.

4. Use the same wrapper for native `pane`, `tab`, and `workspace` management.
   Discover IDs from the target before operating; names and IDs are scoped to
   that session. B and C may each have `reviewer` without conflict. Concurrent
   `bee on` calls have independent sockets and cannot change each other's target.

`bee on` executes the local Herdr CLI with an invocation-private API proxy socket.
Hive routes each API connection to the same selected publication generation; the
target Bee checks its bound session socket and the method allowlist. The native
CLI keeps its protocol checks, startup polling, wait semantics, output formatting,
stdin, stderr, and exit status. Text reads remain text, not Bee JSON envelopes.

The wrapper clears inherited `HERDR_*` context and rejects session overrides,
the `--current` option, and relative `--cwd` option values. Literal prompt or
shell text is preserved. Use explicit target pane/workspace IDs.
Interactive `agent attach`, binary terminal transport, local-file explanation,
worktree commands, plugin administration, and server stop/update are outside this
API route. Native UI sharing remains available for interactive viewing.

Sharing still grants registered members full control of the selected session.
Closing a remote pane affects the real process on its owner. Disabling sharing
closes API connections while local agents keep running. Session replacement or
publisher reconnection invalidates an invocation; discovery on the next command
finds the fresh generation. An interrupted write can already have taken effect:
Bee never replays it. Inspect state before retrying. Native waits observe agent
states, not durable task IDs or guaranteed complete answer transcripts. Multiple
callers retain Herdr's native concurrent input semantics.

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

## Shared terminal sizing

Bee keeps terminal sizes stable while remote viewers have a tab open. The first
remote view pins each pane's current PTY rows and columns; later viewers reuse
those dimensions. Local and remote users retain input access. Background session
discovery does not pin sizes. Leaving a tab, hiding the remote session, disconnecting
or disabling sharing releases that viewer's references; the last viewer leaving
restores Herdr's normal sizing. Herdr and Hive need no upgrade for this feature.

A smaller viewing window can crop content, including a prompt near the bottom;
a larger window can leave unused space. Enlarge the viewing window to see the
fixed grid. Split and zoom operations retain existing terminal sizes while shared;
new panes are pinned when their snapshot arrives. This prevents resize-driven
reflow, not conflicting simultaneous input or application-generated redraws.

The publisher uses the installed Herdr `terminal session control` CLI without
`--takeover`, one helper per pinned terminal, at most 32 across the Bee process.
It reads PTY geometry through the local process ID, `ps`, and a read-only terminal
ioctl, and drains helper frames without retaining terminal history. The published
session's API and client sockets must both remain available. An existing direct
terminal controller is never displaced: a conflicting remote view disconnects
and Bee logs the reason. After the controller is released, reconnect the view.
A live pane losing its sizing helper also disconnects affected viewers; ordinary
pane closure releases that pane without ending the rest of the session.

## Managed connection files

The Bee config directory holds `device_key` (a generated Ed25519 private key with
mode 0600), verified `hive-*.known_hosts`, `ssh_config`, per-target `ssh-connections/*.conf` and `joined.json` (native
setup completion metadata, no credentials). Existing configured identities are
reused. The generated key is unencrypted for unattended local connections and
never leaves the device.

Bee prepends an Include for its own SSH file to `~/.ssh/config`, retaining existing
content and following symlinked dotfiles. Native profiles are created/enabled via
Herdr CLI, never by editing Herdr's profile storage. Repeated joins reuse the
managed target, or a manually added profile resolving to the same address, device
identity and verified host file. Profiles with multiple identities or explicit certificates
are not reused. Prior managed targets stay available when switching Hive. Joining another Hive requires stopping sharing and clearing the
previous session selection first. Existing connections refuse an invitation with
a different host key until the operator verifies and updates the saved trust.

Registration and native setup are separate checkpoints: failed registration leaves
connection settings unchanged but retains the generated key for retry. A failed
native setup leaves the registered connection usable through `bee join`. Removal
can use `herdr machine remove` plus removal of Bee's Include line; device credentials
remain registered at Hive until the administrator revokes the device.
