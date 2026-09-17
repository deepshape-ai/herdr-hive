Hive v0.4.2 restores native Hive connections after upgrading to Herdr 0.9.1.

- Hive now recognizes Herdr's framed SSH bootstrap and validates discovery
  requests by structure and version, so clients no longer stay on `connecting`.

Compatibility: Bee 0.4.0 and compatible Herdr 0.9.0 or newer are supported.
Update the running service with `hive update --state-dir PATH`. Clients briefly
reconnect while device identities, names and sharing settings are preserved.
