Hive v0.4.0 connects native Herdr commands between Bees.

- Agents on any Bee can use `bee on` to manage agents and panes in another
  Bee's shared session, with each command bound to its selected session.
- Unsharing a session or disconnecting its publisher closes remote commands;
  interrupted commands are not replayed after reconnection.
- Administrators can issue one invitation containing the connection details
  and trusted host key needed to join through Bee.

Compatibility: remote commands require Bee 0.4.0 on both devices and unmodified
Herdr 0.9.0; existing terminal sharing and older Bee installations remain
supported. Updating Hive briefly reconnects clients while preserving device
identities, names and sharing settings.
