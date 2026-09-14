# Architecture

Hive and Bee are independent products in one repository. Bee is installed by
publishers; Hive is operated centrally; consumers use unchanged Herdr and OpenSSH.
Each component owns its dependencies and release. Only [protocol v1](../protocol/v1.md)
is shared as a contract; there is no cross-component implementation import.

## Responsibilities

| Component | Modules | Boundary |
| --- | --- | --- |
| Hive | `server`, `registry`, `enrollment`, `herdr` | Authentication, bounded connection ownership, live directory, persistent name assignment and native request adaptation |
| Bee | `app`, `config`, `publisher`, `herdr` | TUI/CLI parity, atomic saved intent, background connection, local session discovery and explicit socket access |

The implementation uses Go and `golang.org/x/crypto/ssh`. Bee's SSH client owns one
outbound transport with multiplexed reverse channels. A custom application channel
keeps the publication restricted to fixed operations; it is not a general reverse
port tunnel. Hive's consumer-facing side speaks ordinary SSH, so OpenSSH and native
Herdr need no replacement. SSH cryptography and flow control are library functions.

The only persistent Hive metadata is a bounded owner-fingerprint-to-visible-name
map. Atomic JSON plus a single-process file lock is sufficient for this small
registry; adding SQLite would add maintenance without a current query requirement.
Live shares and streams are memory-only. No terminal history, event database,
message queue, metrics database or web server is introduced.

## Resource and identity model

- **Device:** a dedicated SSH public key. Use the same key for Bee and native
  consumption on one machine. A hostname is only a suggested display name.
- **Manual rule:** an explicitly selected Herdr named session plus a random local
  rule key. It remains selected while sharing is globally off.
- **Share:** a stable opaque SSH username derived from device identity and rule key.
  The share routes to exactly one selected server; it is not a login credential.
- **Connection:** a live registration and zero or more native consumer streams.
  Reconnect creates new streams; it never replays old input.

All registered Hive devices receive full control of published named sessions.
Unselected named sessions are not registered. This does not isolate the agent's
system permissions or subdivide a selected session's workspaces. A session may
continue to change or restart under the same selected name. Fine ACLs, read-only
roles and control leases are intentionally absent from this product scope.

Hive hides a device's own publications from the directory and aggregate endpoint, and rejects self-routing.
Native Herdr's locally saved profiles are owned by Herdr. Bee invokes the native
CLI to add/enable a profile; it never edits Herdr's profile files.
Different keys represent different devices even when they share an IP or hostname.

## Publication and connection lifecycle

0. An administrator can package the reachable address, persistent public host key
   and enrollment token into a `hinv1-` invitation. Bee creates/reuses a dedicated
   device key, verifies the provided host key, enrolls, saves config, then installs
   a managed OpenSSH Include and invokes native `machine add`. Native setup can
   resume without the invitation; successful enrollment does not enable sharing.
   The advanced manual path also accepts a raw token. Bee proves possession of its SSH
   key and redeems the token over a verified short SSH connection before saving
   configuration; Hive reserves usage, then persists the public key.
1. The connection uses the same verified Hive host key and device identity for
   both publication and consumption, whether configured manually or by invitation.
2. Bee discovers named sessions through the local Herdr CLI. The owner chooses
   running sessions. Selection alone does not enable sharing.
3. Enabling starts one background publisher. Bee binds the selected API/client
   socket identities and authenticates to Hive.
4. Hive resolves name collisions and registers selected targets on that transport.
   An acknowledged registration returns stable IDs and the resolved display name.
5. Another device adds one `User hive` SSH alias through native `herdr machine add`.
   Hive aggregates visible publications under `[Bee name/session] workspace`,
   omitting `/default` for the default session. Labels keep the source first and
   do not change when other sessions connect or disconnect; resource IDs and
   owner workspace names are unchanged.
   A `User s-…` alias optionally selects one share; no remote session override is used.
6. Hive recognizes supported native bootstrap requests and maps them to fixed Bee
   operations. Bee returns live Herdr status or opens the selected client socket.
7. Bee inspects native hello, navigation, response and snapshot frames to pin
   viewed PTYs through Herdr terminal controllers. Other frames stream unchanged.
   The aggregate endpoint decodes
   the frozen generation-1 codecs, namespaces resources, routes operations and
   retains bounded complete surfaces for switching. Neither path records terminals.
8. Closing a consumer attachment closes its streams. Disabling publication closes
   all its streams before acknowledgement. Local Herdr processes survive.
9. Hive loss disconnects remote streams. Bee retries with bounded exponential delay;
   Hive restarts with an empty online directory. Valid re-registration restores the
   same IDs. Unknown protocol/config versions fail closed.

A saved manual rule deliberately identifies a named session, not an agent PID.
Within a connected publication, changed socket identities reject new streams.
Refresh publication after a local server replacement. Bytes already written into
Herdr or terminal buffers cannot be recalled. Configuration changes currently
reconnect the entire Bee publication, favoring simple ownership over partial updates.

## Native compatibility

The integration target verified in this implementation is Herdr 0.9.0. Native
bootstrap scripts are identified by exact requests or known discovery hashes.
Unknown scripts are rejected, never executed. Bee stages shell activation and
uses private surface barriers while forwarding native terminal input and output.
The aggregate codec and limits are specified in protocol/v1.md and
hive/README.md; unsupported aggregate operations remain available by direct sharing ID.

| Operation | Implementation / guarantee |
| --- | --- |
| Add remote machine, display colleague label | Native `machine add`; tested |
| Terminal input and text paste | Native client protocol; duplex input/output tested |
| Interactive agent questions and approvals | Same terminal input path and full session authority; no separate approval ACL |
| Workspace/tab/pane management | Native server semantics; all objects inside selected session are accessible |
| Resize, scrollback and copy | Bee pins viewed PTY sizes; scroll/copy stay native, aggregate IDs and surfaces are translated |
| Concurrent consumers / local owner input | Native Herdr concurrency semantics; no additional single-writer lease |
| Agent state | Native Herdr events and detection; no proxy-pane metadata impersonation |
| Images, terminal extensions, files | Whatever the supported Herdr native transport implements; not independently certified here. Hive does not add SFTP/SCP |
| Close local machine profile | Native detach; does not stop owner's server |
| Close/kill remote pane | Native remote operation, including destructive effects allowed by full control |
| Restart/resume agent | Available native UI behavior; Hive does not launch or resume agents |
| Remote CLI/API routing | Exactly the selected Herdr release's native support. v0.9.0 does not gain newer `--machine agent` commands through Bee |
| Remote Herdr install/upgrade, session override | Rejected by the bootstrap allowlist; upgrade explicitly on the owner host |

The main maintenance risk is the native bootstrap contract, which Herdr does not
expose as a stable plugin transport API. Changes are isolated in
`hive/internal/herdr` and verified using native integration tests. Compatible native
payload changes need no relay rewrite; arbitrary future versions are not promised.

## Resource control and observability

[Hive's resource envelope](../hive/README.md#resource-envelope) defines defaults,
window capacity, persistent limits and OS-level protections. Connection and channel
budgets are global, so adding consumers cannot create unbounded relay buffers.
Bee additionally permits at most 32 sizing helper processes across published
sessions. Each helper drains rendered frames (8 MiB record limit) and is released
with its last viewer reference. Helpers add local process/rendering overhead; see
[Bee sizing behavior and limitations](../bee/README.md#shared-terminal-sizing).

Slow consumers backpressure the originating stream. Closing either side unblocks
both copy directions and returns its capacity slot.

Enrollment has four slots within the transport budget, one request and a
15-second deadline. Administrator token hashes and service-owned enrollment
usage/public keys are separate bounded files; offline token commands do not
compete for the running service's state lock. See [enrollment operation](../hive/README.md#enrollment-tokens).

Inspection is a private local Unix socket with text, watch and JSON CLI clients.
It reads live metadata and counters, never terminal content. This avoids an extra
web service, authentication surface or metrics store. Memory readings explicitly
distinguish Go heap/runtime allocation from OS RSS.

## Sources

- [Herdr native machine CLI](https://github.com/herdrdev/herdr/blob/v0.9.0/src/cli/machine.rs)
- [Herdr SSH bootstrap and attach](https://github.com/herdrdev/herdr/blob/v0.9.0/src/remote/attach.rs)
- [Herdr remote client socket bridge](https://github.com/herdrdev/herdr/blob/v0.9.0/src/remote/host_unix.rs)
- [Herdr plugin manifest](https://github.com/herdrdev/herdr/blob/v0.9.0/src/app/api/plugins/manifest.rs)
- [Go SSH implementation](https://pkg.go.dev/golang.org/x/crypto/ssh)

## Component updates

Hive and Bee remain independently installed and released. A small standard-library
Go module at `internal/update` shares only release discovery, verification and
executable replacement. Neither product imports the other's application code.
Source builds use the repository-local module replacement; packaged users need
only their component executable. CI tests this module on both supported systems.

[Updates](UPDATING.md) download while the current process continues serving, then
replace its process image. SSH encryption state and goroutines are not migrated;
connections briefly disconnect. This avoids a second relay generation, connection
handoff protocol or supervisor service. No Herdr core changes are required.

The `hive/internal/gateway` module owns each viewer's projection and routing event
loop. Server adapters own SSH channels and hard write deadlines. Source loss
removes its metadata and pending operations; other sources continue. Response IDs
are bound to their originating source. Live image assets and terminal input modes
are retained for coherent switching, within the documented resource limits.
