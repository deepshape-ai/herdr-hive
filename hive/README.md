# Hive

Hive runs on the shared machine. It is an application SSH service with no ordinary
shell or arbitrary forwarding. One process handles SSH, the live directory,
native Herdr bootstrap and streaming. No database service is required.

## Start

Collect one dedicated SSH **public** key per device into an authorized-keys file.
Use plain public-key lines; authorized_keys options and SSH certificates are not
part of v1. Never upload employee host login keys or private keys. Registered
devices receive full access to published sessions, except their own.

```sh
mkdir -m 700 ./hive-state
hive --listen 0.0.0.0:2222 \
  --state-dir ./hive-state \
  --authorized-keys ./authorized_keys
```

Hive creates a private host key on first start and prints its fingerprint. Verify
that fingerprint through an administrator before adding the key to each client's
`known_hosts`. Key scanning alone is not verification. Back up the host key and
`names.json` to preserve trust and collision-resolved names after migration.

For Linux, [deploy/hive.service](deploy/hive.service) provides a systemd example
with memory, CPU and log-rate bounds. Install the binary and authorized-keys file
at its documented paths before enabling it. The service is optional; foreground
execution is sufficient for evaluation.

## Inspect

```sh
hive inspect --state-dir ./hive-state
hive inspect --state-dir ./hive-state --watch
hive inspect --state-dir ./hive-state --json
```

The private Unix inspection socket is accessible to the service account and host
administrator. It remains separate from the SSH channel budget. It shows uptime,
connections and limits, rejected capacity requests, Go heap/runtime allocation,
registry bytes, active shares, connection counts and cumulative bytes in each
direction. Runtime allocation is **not OS RSS**. Counters reset when a publication
or Hive restarts; no history database or terminal preview is maintained.

## Native consumer setup

Use the same dedicated device key for publishing and consuming:

```sh
ssh -p 2222 -i ~/.ssh/hive_device hive@hive.example.internal 'list --json'
```

The list contains other online devices' shares. Copy a returned `id` into a normal
SSH alias (example ID below is illustrative):

```sshconfig
Host hive-colleague
    HostName hive.example.internal
    Port 2222
    User s-0123456789abcdef01234567
    IdentityFile ~/.ssh/hive_device
    IdentitiesOnly yes
    StrictHostKeyChecking yes
```

Then use Herdr itself:

```sh
herdr machine add hive-colleague --label "Colleague / project"
```

The label shown in Herdr is the colleague's name, not Hive. One alias/profile
represents one shared named session. The alias already selects the published
session: **omit `--remote-session`**. Hive rejects attempts to retarget the alias.

Own shares are hidden by device key, not by source IP or hostname. Using a different
key on the same machine represents a different device. Existing locally saved
machine profiles are not silently edited; remove obsolete profiles with Herdr's
native `machine remove`.

## Resource envelope

Defaults: 64 simultaneous SSH transports, 16 application channels globally, at
most 32 published sessions per device, and 1,024 persisted device names. Override
only the first two with `--max-connections` and `--max-channels`. Bootstrap and
terminal connections both consume the channel budget; rejection is explicit.

The SSH library bounds channel windows to 2 MiB per direction. Two relay legs
therefore contribute at most approximately 4 MiB of receive-window capacity per
active forwarded channel in Hive, plus small copy buffers, SSH transport queues,
crypto and Go runtime overhead. This is a capacity envelope, not a resident-memory
promise. Sixteen channels give roughly 64 MiB of application receive-window
capacity. Slow readers exert backpressure; Hive never accumulates a full session
transcript. Herdr remains responsible for its own terminal history and output policy.

Control payloads and the authorized-keys file are limited to 64 KiB. Bootstrap
and channel-open operations have deadlines. Dead network writes time out. The
registry is one atomically replaced JSON file, capped by device count; Hive stores
no audit/event history. Normal operation logs startup only. Inspect exposes live
counters instead of writing per-frame logs.

The systemd example sets `MemoryHigh=192M`, `MemoryMax=256M`, `CPUQuota=100%`, and
Go's soft memory target to 160 MiB. These protect the host, but overload or an OS
kill interrupts remote sessions. Local agents survive. Tune limits after measuring
your workload; no unmeasured concurrency or latency guarantee is implied.

## Upgrade, removal and recovery

Stop Hive, replace the binary and restart using the same state directory. Bees
reconnect automatically; native consumers reconnect according to Herdr behavior.
An offline target never starts a Herdr server on Hive. Old terminal input is not
replayed. One process owns the state directory through a file lock.

Restore the previous binary and state backup to roll back. Removing the service
and its directory removes Hive only; it does not stop employee agents. Removing
an authorized key blocks new authentication. Restart Hive if existing transports
for that key must be terminated immediately.
