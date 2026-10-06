# Verification evidence

2026-09-13. Product implementation tests, not the earlier stock-sshd relay experiment.
No production deployment has been performed. Real test-host
addresses, credentials and terminal recordings are excluded from this repository.

## Issue #1 recovery repair (Hive 0.4.5 / Bee 0.4.3)

- The old `c884dc0` implementation fails assertion-based regressions: rapid
  last-view disconnect destroys its controller; repeated source-open failure
  performs six opens in 5.5 seconds; an unnegotiated native codec reopens three
  times instead of quarantining once. Tests use the old public API and compile
  on the baseline, so these are behavioral failures, not interface errors.
- Real Herdr 0.9.1 with baseline Hive/Bee binaries fails the new attachment
  acceptance on exact controller reuse. The final candidate passes `native.py`,
  `attachments.py`, `remote_cli.py` and `panel.py` sequentially against official,
  checksum-verified Herdr 0.9.0, 0.9.1, 0.9.2 and 0.9.3 on macOS arm64.
  Each attachment run passes 100 rapid last-viewer reconnects with the same eight
  controller processes, unchanged complete PTY geometry, eventual local-size
  restoration and no persistent conflict retry during the 12-second observation.
- The full Herdr 0.9.1 native acceptance also passes with an unchanged Bee built
  from baseline `c884dc0` and the candidate Hive, including later publisher
  executable upgrade. Classified quarantine requires the optional report from
  an updated Bee; older publishers remain interoperable.
- Final Bee/Hive race suites and vet pass; the shared updater race suite/vet,
  shell syntax check and all nine offline installer tests also pass. The Bee
  cross-view membership/fresh-view recovery tests pass 100 repetitions across
  `-cpu=1,2`; the actual SSH sideband and pending-open tests pass 20 repetitions
  across both CPU settings. Candidate native packages build successfully.
- A candidate-wide Bee failure cooldown was rejected during implementation:
  real native acceptance and an assertion-red fresh-view transition test showed
  it poisoning a new viewer after the controller recovered. Final Bee retains
  resource grace, not failure history; that transition now passes. Cross-view
  membership tests also cover fresh low-revision projections, stale viewers and
  removed idle panes without evicting healthy controls.
- CI includes the attachment regression in the existing pinned/scheduled native
  matrix. These are local verification results, not claims of hosted CI execution
  or a new published release.
- This establishes controller reuse and bounded aggregate recovery, not the
  reporter's precise initial failure or physical display behavior. Native
  `terminal session control` still acquires an exclusive size controller; its
  initial attach/final detach can schedule native layout and redraw. No passive
  mirror capability is assumed or added to Herdr.

## Passed

- `make test`: both independent Go modules pass under the race detector.
- `make vet`: both modules pass Go vet.
- Native local integration: three isolated Herdr 0.9.0 publishers, stock consumer
  `machine add`, actual native TUI input/output in three directions, duplicate
  names, unsupported shell/unshared-route rejection, and disable without stopping
  local sessions.
- Bee plugin: actual Herdr plugin link and enable/disable action invocation; TUI
  name change observed through the same CLI configuration surface.
- Two physical hosts: macOS publisher/consumer and Linux publisher/consumer, Hive
  on Linux. Both native `machine add` directions and both terminal directions pass.
- Own shares: hidden from the directory and rejected when the publishing device
  attempts to consume its own route with the same key.
- Hive restart: both publishers reconnect and retain their share IDs. Local
  sessions survive. Temporary local/remote installations are removed afterwards.
- Fault recovery: blocked local Unix I/O and SSH-agent cancellation, missing TUI
  selections, null/trailing configuration rejection, consumer-only disconnects
  under both backpressure directions, and bounded SSH close-ack cleanup have
  regression coverage.
- Resource test: a nonreading consumer backpressures a 128 MiB synthetic producer;
  heap growth remains below the 32 MiB test guard (including both test clients and
  server). The global channel budget rejects excess channels. Closing publication
  releases blocked copy operations and capacity. This runs under the race detector.

## Upgrade verification

- Archive integrity, unsafe paths and symlinks, per-installation locking, cancelled
  downloads, stable component selection, prerelease downgrade prevention and
  bounded binary version output have automated coverage.
- A native integration run starts test binaries reporting 0.0.1, replaces them
  with the current build, requests both process restarts and confirms their new
  runtime versions with unchanged PIDs and share IDs. Native terminal input/output
  works after reconnect and local Herdr sessions survive. Test processes and
  temporary installations are removed.
- Restart is requested through private local IPC. A privileged Hive updater never
  signals a PID supplied by the service. Unexpected control operations are rejected.

## Herdr compatibility acceptance (Hive 0.4.4 / Bee 0.4.2)

- Local macOS arm64 runs of `native.py`, `remote_cli.py` and `panel.py` passed
  against official Herdr 0.9.0, 0.9.1, 0.9.2 and 0.9.3 binaries.
- The same native suite passed with checksum-verified published Bee 0.4.1
  publishers and the candidate Hive, including subsequent Bee process replacement;
  the aggregate fix does not require a simultaneous Bee upgrade.
- Modern viewers offering optional render flags retained all three publishers
  during 80 rows of scrolling output, with 80 or 81 independently decoded surface
  updates. Switching away and back required new destination-specific rendered
  scenes, not the pre-switch cached screen.
- Inspect accounted for three additional upstream streams during the viewer's
  lifetime and zero after closure. Real CLI text separately displayed the viewer,
  upstream and ordinary channel budgets.
- Explicit remote pane focus succeeded; a subsequent `--current` request was
  rejected without changing the owner's focused pane. Literal command data and
  command-specific option boundaries have regression coverage.
- New public gateway tests failed against the previous implementation for modern
  codec negotiation, silent initialization recovery, incremental patches and late
  responses with consumer request ID reuse; Bee context tests also failed there.
  The exact candidate patch passed all three Go modules' race tests and vet in
  an isolated worktree, plus all nine offline installer tests.
- The source-switch/graphics regression passed 2,000 race-enabled repetitions
  across `-cpu=1,2`. Its duplex owner fixture now reads input independently of a
  serialized output queue, matching native owner behavior; a synchronous
  zero-buffer pipe had caused a test-only ACK/input deadlock during release CI.
  Copy-only, internal-capacity and wording assertions were removed without
  removing the source-switch, checksum preservation or authentication checks.
- All eight component/platform archives built successfully and their entry
  allowlists, executable architecture and Bee manifest versions were inspected;
  macOS arm64 executables also passed real version/help invocation.
- CI now tests pinned 0.9.0 through 0.9.3 releases and separately checks the
  minimum plus latest stable release weekly or on manual dispatch.

These runs cover the supported frozen fallback, not arbitrary future protocol
generations, optional delta/scroll codecs, physical display latency or a capacity
soak. Hosted CI and published artifacts require separate release verification.

## Measurements

One LAN test used two connected native terminals and sampled 20 command completions
on one direction with no concurrent bulk output. P95 was **18.95 ms** from sending
input to reconstructing the matching ANSI terminal screen. Artificial send delay
was disabled. This is not physical monitor latency and is not an SLA or a loaded
concurrency claim. Terminal screen reconstruction is necessary: incremental ANSI
updates do not always contain complete output strings in each byte chunk.

At the sample point Hive reported 4 SSH transports and 2 channels, approximately
**1.18 MiB Go heap** and **12.28 MiB runtime allocation**. These are not OS RSS,
peak memory, or a capacity estimate. Use `hive inspect --watch` and host-level
monitoring during workload sizing. Global windows and the deployment memory
limits bound overload risk separately from these light-load observations.

## Limits of the evidence

No physical 64-host test, long-duration soak, image/file-transfer certification,
agent-specific approval/resume test, or complete native UI feature audit has been
performed. Native transport reuse does not constitute evidence that every future
Herdr version or terminal extension is compatible. The bootstrap adapter has
unit coverage for the unframed 0.9.0 protocol and the framed 0.9.1 protocol;
compatible discovery scripts advertising version 0.9.0 or newer are accepted by
grammar rather than exact hash. Unknown bootstrap requests fail closed.

See [integration instructions](../tests/integration/README.md) and the checked-in
tests for reproducible setup. CI defines independent macOS/Linux Go checks; a local
pass is not evidence that hosted GitHub Actions have run.
