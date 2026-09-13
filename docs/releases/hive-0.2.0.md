Hive v0.2.0 lets administrators admit new Bee devices with enrollment tokens instead of manually collecting public keys.

1. **Enrollment tokens.** Issue, list and revoke tokens with `hive enroll`; optional expiry and device limits take effect without restarting Hive.
2. **Enrollment status.** `hive inspect` reports whether registration is enabled and counts accepted and rejected requests.

Enable registration with `--authorized-tokens PATH`. Tokens can have unlimited lifetime and uses; revoke enrolled devices separately by removing their registered public keys. Existing Bee publishers and native consumers remain compatible.

Update with `hive update --state-dir PATH`, then add the optional token file to the service's startup arguments. Activation briefly disconnects clients while Bees reconnect; local agents continue running.

[Token setup and operation](https://github.com/deepshape-ai/herdr-hive/blob/hive/v0.2.0/hive/README.md#enrollment-tokens)
