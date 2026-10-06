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
8. Closing a consumer attachment closes its streams and releases its sizing
   references. Bee owns the helpers independently and retains unused controls for
   two seconds to absorb rapid reconnects. Disabling publication closes streams
   and all helpers before acknowledgement. Local Herdr processes survive.
9. Hive loss disconnects remote streams. Bee retries with bounded exponential delay;
   Hive restarts with an empty online directory. Valid re-registration restores the
   same IDs. Unknown protocol/config versions fail closed.

A saved manual rule deliberately identifies a named session, not an agent PID.
Within a connected publication, changed socket identities reject new streams.
Linux publications retain path descriptors for both socket inodes until all
streams have closed, preventing unlink/rebind from reusing an old inode identity.
Bee checks bindings every second and withdraws a stale publication so the retry
loop can bind the replacement endpoints automatically. Bytes already written into
Herdr or terminal buffers cannot be recalled. Configuration changes currently
reconnect the entire Bee publication, favoring simple ownership over partial updates.

Aggregate recovery belongs to Hive, not Bee's native adapter. Each viewer retains
bounded failure state keyed by share ID and publication generation. Transient
failures retry after 1, 2, 4, 8 and at most 16 seconds; unchanged catalog entries
and metadata handshakes do not erase this history. A persistent adapter/protocol
failure quarantines only that generation for the affected viewer. A new generation
or viewer can try again; ten seconds of active rendered progress permits recovery
history to reset. Input and operations are never replayed. Bee communicates
classification through an optional SSH sideband, keeping native bytes unchanged.
Bee does not cache a viewer's adapter failure across connections: an explicitly new
viewer uses current native resources, even if an earlier view hit a controller
conflict. Controller idle grace is resource reuse, not failure cooldown.

## Native compatibility

The native bootstrap adapter accepts compatible Herdr 0.9.0 and newer. It
recognizes exact operational requests, preserves the framed output protocol used
since 0.9.1, and validates discovery scripts by constrained grammar and semantic
version instead of whole-script hashes. Unknown scripts are rejected; accepted
scripts are inspected as data and never executed. Bee stages shell activation and
uses private surface barriers while forwarding native terminal input and output.
The aggregate endpoint constructs its own generation-1 offer from the four
implemented frozen codecs, never copying optional rendering offers from a viewer.
Compatible future owners can select these codecs as a fallback; an incompatible
generation or selected codec fails explicitly. Unknown non-rendering sidebands
are opaque, while unsupported rendering controls cannot advance the retained
baseline. The aggregate codec and limits are specified in protocol/v1.md and
hive/README.md; unsupported aggregate operations remain available by direct sharing ID.

| Operation | Implementation / guarantee |
| --- | --- |
| Add remote machine, display colleague label | Native `machine add`; tested |
| Terminal input and text paste | Native client protocol; duplex input/output tested |
| Interactive agent questions and approvals | Same terminal input path and full session authority; no separate approval ACL |
| Workspace/tab/pane management | Native server semantics; all objects inside selected session are accessible |
| Resize, scrollback and copy | Bee pins viewed PTY sizes; scroll/copy stay native, aggregate IDs and surfaces are translated |
| Concurrent consumers / local owner input | Native Herdr concurrency semantics; no additional single-writer lease |
| Agent state | Native Herdr snapshot state is preserved; the aggregate endpoint suppresses remote completion/attention sounds and toasts |
| Images, terminal extensions, files | Whatever the supported Herdr native transport implements; not independently certified here. Hive does not add SFTP/SCP |
| Close local machine profile | Native detach; does not stop owner's server |
| Close/kill remote pane | Native remote operation, including destructive effects allowed by full control |
| Restart/resume agent | Available native UI behavior; Hive does not launch or resume agents |
| Remote CLI/API routing | `bee on B/session -- herdr …` supplies a private API proxy socket to the unchanged native CLI; each invocation pins one publication generation |
| Remote Herdr install/upgrade, session override | Rejected by the bootstrap allowlist; upgrade explicitly on the owner host |

The main maintenance risk is the native bootstrap contract, which Herdr does not
expose as a stable plugin transport API. Changes are isolated in
`hive/internal/herdr` and verified using native integration tests. Compatible native
payload changes need no relay rewrite; arbitrary future versions are not promised.

## Agent-to-agent automation

Every Bee can both publish and consume. `bee targets` exposes the existing Hive
directory with API capability and publication generation. `bee on` resolves one
share, creates a private local proxy socket and runs the native Herdr CLI with
clean caller context. The local CLI's API requests travel over SSH to Hive, then
over the target's reverse connection into its bound Herdr API socket. There is no
global selected remote and no merged agent-name namespace. Native multi-request
startup polling stays in one session, even when other calls target another Bee.

The target Bee enforces a method allowlist. Hive pins the publication generation,
excludes self-routing, and tears down API channels on unpublication. Bee also
checks the local socket identity during long waits. Reconnect never replays a
request. Native response JSON and exit codes remain authoritative; this adds no
task scheduler, inference service, transcript store, or exactly-once task promise.
See [the wire contract](../protocol/v1.md#session-scoped-cli-api-extension) and
[agent usage](../bee/README.md#remote-agent-automation).

## Resource control and observability

[Hive's resource envelope](../hive/README.md#resource-envelope) defines defaults,
window capacity, persistent limits and OS-level protections. Connection and channel
budgets are global, so adding consumers cannot create unbounded relay buffers.
Bee additionally permits at most 32 sizing helper processes across published
sessions, including idle helpers. Each helper drains rendered frames (8 MiB record
limit). Last-reference release starts a two-second grace period; rapid reacquisition
reuses the exact helper, while capacity pressure can retire unused controls early.
Pane removal and publication closure release helpers without grace. ClientShell
snapshot revisions are per viewer, not publication-wide; cross-view pane deletion
is reconciled through the local authoritative API rather than comparing revisions.
Helpers add local process/rendering overhead; see
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
loop. Server adapters own SSH channels and hard write deadlines. Source opening
and hello writing have a ten-second deadline, followed by ten seconds for welcome
and the first snapshot. Source loss removes its metadata and pending operations;
other sources continue. Unique upstream request IDs are bound to their source
and mapped back to consumer IDs. Timed-out ordinary operations retain bounded
identities until their final response or source removal, so late chunks cannot
disconnect a healthy source or reach a reused consumer ID. No operation is replayed.
Active plain patches remain incremental only with an exact displayed baseline;
complete scenes and live graphics are retained for coherent switching. Viewer
streams and their additional upstream legs are accounted separately in inspect.
