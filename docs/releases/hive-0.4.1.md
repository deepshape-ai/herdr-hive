Hive v0.4.1 allows more Herdr windows to connect through the aggregate endpoint.

- Hive now accepts eight simultaneous aggregate connections instead of two,
  so two connected windows no longer prevent another member from joining.

Compatibility: Bee 0.4.0 and Herdr 0.9.0 remain supported. Update the running
service with `hive update --state-dir PATH`. Clients briefly reconnect while
device identities, names and sharing settings are preserved.
