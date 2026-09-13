# Verification evidence

2026-09-13. Product implementation tests, not the earlier stock-sshd relay experiment.
No production deployment or public release has been performed. Real test-host
addresses, credentials and terminal recordings are excluded from this repository.

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
Herdr version or terminal extension is compatible. The current bootstrap adapter
is verified against Herdr 0.9.0. Unknown bootstrap requests fail closed.

See [integration instructions](../tests/integration/README.md) and the checked-in
tests for reproducible setup. CI defines independent macOS/Linux Go checks; a local
pass is not evidence that hosted GitHub Actions have run.
