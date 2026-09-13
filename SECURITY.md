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
