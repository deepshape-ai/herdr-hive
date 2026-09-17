Hive v0.4.3 keeps remote completion notifications local to their source machine.

- The aggregate `User hive` endpoint now suppresses remote agent completion and
  attention sounds and toasts immediately, without a configuration option.
- Remote agent status remains visible in merged Hive workspaces, and direct
  `User s-…` connections retain the upstream session's notification behavior.

Compatibility: Bee 0.4.0 and compatible Herdr 0.9.0 or newer are supported.
Update the running service with `hive update --state-dir PATH`. Clients briefly
reconnect while device identities, names and sharing settings are preserved.
