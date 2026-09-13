# Hive

Hive runs on the shared machine. It is an application SSH service with no ordinary
shell or arbitrary forwarding. One process handles SSH, the live directory,
native Herdr bootstrap and streaming. No database service is required.

## Start

For download and checksum commands, start with the
[Hive Quickstart](../README.md#1-install-hive-on-the-shared-machine).

Install the newest stable release:

```sh
curl -fsSL https://raw.githubusercontent.com/deepshape-ai/herdr-hive/main/install.sh | sh -s -- hive
```

The default location is `~/.local/share/herdr-hive`. The `hive` commands below
abbreviate the full executable path; no PATH changes are made.

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

## Enrollment tokens

Administrators can admit devices with a token instead of collecting public keys:

```sh
hive enroll issue --tokens ./authorized_tokens --label onboarding
# Optional limits; omitted or zero means no expiry / unlimited devices:
hive enroll issue --tokens ./authorized_tokens --ttl 24h --max-uses 5
hive enroll list --tokens ./authorized_tokens --state-dir ./hive-state
hive enroll revoke t-0123456789abcdef --tokens ./authorized_tokens
```

`issue` prints JSON with a `secret` beginning with `hreg-`, displayed only once.
The administrator file stores SHA-256 hashes. Share the secret through a trusted
channel. Start Hive with `--authorized-tokens ./authorized_tokens` to enable
registration; without this flag enrollment is disabled. The required external
`authorized_keys` file may be empty. To start with no tokens, initialize the token
file to `[]` (an empty file or `null` is invalid).

Token commands lock `<tokens>.lock` and atomically replace the token file. New
files are mode 0600; existing modes are preserved. The service needs read access:
for the supplied DynamicUser unit, use an administrator-owned directory and a
root-owned 0644 token file. Issue and revoke take effect on the next request,
without a restart. The service never writes the administrator file; `list` reads
usage optionally, and these offline commands never write the service state.

On a new device, verify Hive's host key and run:

```sh
bee configure --hive hive.example.internal:2222 \
  --identity ~/.ssh/hive_device --known-hosts ~/.ssh/hive_known_hosts \
  --token hreg-REPLACE_WITH_ISSUED_SECRET
```

The key is added to `state-dir/registered_keys`. Retrying an already registered
key with a valid, unexpired token succeeds without consuming another use.
Expiry and token revocation stop future enrollments, including retries; they do
not remove enrolled devices. To revoke an enrolled device, stop Hive, delete its public-key line from
`registered_keys`, then start Hive. Stopping prevents concurrent enrollment from
overwriting the edit and terminates existing connections. Remove any copy from
the external `authorized_keys` too; that externally managed file can be edited
online, blocking new authentication.
A device with a still-valid token can register again, so revoke its token too.
Do not edit the service-owned `registered_keys` while Hive is running.

Usage is stored in `enrollment.json` before the public key is written. Failed key
writes roll back the new reservation. A crash may leave a reservation; retrying
the same key resumes it without another use. Back up both files with the host
key and names registry. Do not delete usage state to reset an active token's limit.

## Inspect

```sh
hive inspect --state-dir ./hive-state
hive inspect --state-dir ./hive-state --watch
hive inspect --state-dir ./hive-state --json
```

The private Unix inspection socket is accessible to the service account and host
administrator. It remains separate from the SSH channel budget. It shows uptime,
connections and limits, rejected capacity requests, Go heap/runtime allocation,
registry bytes, enrollment enabled/accepted/rejected counters, active shares, connection counts and cumulative bytes in each
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

Control payloads, each key file, token file and enrollment usage file are limited
to 64 KiB, with at most 1,024 tokens or keys per store. Invalid files fail closed;
external and enrolled key sources are validated independently. Enrollment uses
at most four authenticated transports within the global connection budget,
one request per connection, no channels, and a 15-second connection deadline. Bootstrap
and channel-open operations have deadlines. Dead network writes time out. The
registry is one atomically replaced JSON file, capped by device count; Hive stores
no audit/event history. Normal operation logs startup only. Inspect exposes live
counters instead of writing per-frame logs.

The systemd example sets `MemoryHigh=192M`, `MemoryMax=256M`, `CPUQuota=100%`, and
Go's soft memory target to 160 MiB. These protect the host, but overload or an OS
kill interrupts remote sessions. Local agents survive. Tune limits after measuring
your workload; no unmeasured concurrency or latency guarantee is implied.

## Update and removal

```sh
# Use the same executable path as the service. For the supplied systemd unit:
sudo /usr/local/bin/hive update --state-dir /var/lib/herdr-hive
# A user-owned foreground installation uses its own --state-dir, without sudo.
```

Hive downloads and verifies its newest stable release while serving existing
connections, then replaces the executable and requests an in-process restart.
The command confirms that the inspection endpoint reports the new version.
The service retains its PID, arguments, host key, registered names and state
location. Bees reconnect automatically; consumers reconnect according to Herdr
behavior. Active remote connections briefly disconnect during the switch. Local
agents keep running and old input is never replayed. The daemon does not need
write access to its binary: the administrator's update command performs installation.

For an offline installation, `hive update` updates only the binary and reports
`active: false`. Omitting `--state-dir` never guesses which running service to
restart. `SIGHUP` (or `systemctl reload hive` when installed as `hive.service`)
re-executes the installed binary with the same arguments. See
[upgrade behavior](../docs/UPDATING.md) for bounded downloads and failure handling.

Removing the service and its directory removes Hive only; it does not stop
employee agents. Removing an authorized key blocks new authentication. Restart
Hive if existing transports for that key must be terminated immediately.
