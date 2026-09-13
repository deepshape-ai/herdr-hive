# Security boundaries

Hive is an application SSH server, not a system SSH gateway. It does not provide
an OS shell, agent forwarding, SFTP, SCP, or arbitrary TCP/Unix-socket forwarding.
Bee registers only selected **Herdr named sessions**. A selected session includes
all its workspaces, panes, agents and future changes. This is full control, not a
sandbox, read-only view, or per-agent permission boundary.

Use one Hive-only SSH identity per publishing device. The same identity is used
for that device's native consumer connections; Hive then hides and rejects its
own publications. Separate keys on the same physical machine are separate devices.
Do not reuse a key that grants login to another employee's machine.

Every registered Hive member may fully control every published session except
its own publications. Members and the Hive administrator are trusted. The
administrator can access data in transit: encryption terminates at Hive. No
terminal contents are intentionally persisted. Process/core dumps and external
logging/monitoring are the deployer's responsibility. The provided systemd unit
limits memory, CPU, process count and journal rate.

Removing a key from authorized_keys prevents new SSH authentication. To terminate
already-authenticated transports immediately, restart Hive. Disabling Bee closes
its publication transport and attached consumers. Bytes already delivered to
Herdr or the terminal cannot be recalled. Local agents keep running.

Native bootstrap is an allowlist, not a shell interpreter. Unknown requests fail
closed. Bee never executes remotely supplied commands. A client cannot select
another local socket path. The selected named session may be replaced or restarted
under the same name; an enabled manual rule intentionally continues to refer to
that name. Within a connection, socket replacement rejects new stream opens.

Please report suspected security problems privately to the repository owner.
Do not attach private keys, terminal contents or internal host addresses to issues.

## Enrollment tokens

An enrollment token admits a device to full member access, including control of
other members' published sessions. Distribute it only through a trusted channel.
Tokens use 256 bits of randomness and are stored as SHA-256 hashes in an
administrator-owned file that the service can only read. Unlimited lifetime and
uses are explicit supported defaults; TTL, device limits and revocation can
restrict onboarding. Token revocation does not revoke devices already enrolled.

Enrollment proves SSH key possession and verifies the Hive host key before
sending the secret. Its isolated SSH user has no owner permission or channels.
One request, a 15-second lifetime and four enrollment slots bound authenticated
registration work; the shared SSH handshake budget remains susceptible to
connection exhaustion. Enrollment rejection does not reveal its cause.

Bee does not persist or echo the token in settings. CLI arguments may appear in
shell history and local process inspection; avoid retaining command history with
secrets. The TUI masks its field and executes configuration in process, keeping tokens
out of child process arguments. Administrators
must protect token-file writes and back up usage state with registered keys;
rolling back or deleting usage state can restore spent capacity. Stop Hive before editing its service-owned `registered_keys`, then start it
again; this avoids racing new enrollments and terminates existing connections.
Revoke the token too to prevent re-enrollment.
