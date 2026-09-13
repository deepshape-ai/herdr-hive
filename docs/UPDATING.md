# Updates

```sh
herdr plugin action invoke update --plugin herdr.bee
sudo /usr/local/bin/hive update --state-dir /var/lib/herdr-hive
```

Bee also provides an Update action and a settings-pane entry. Use the same device
user and plugin installation used for normal publishing. Hive administrators use
the exact service executable and its state directory. No consumer setup changes
are needed. Hive and Bee are updated independently.

## What happens

1. Lock this installation against another updater.
2. Find the highest stable version of this component on GitHub, excluding drafts
   and prereleases. There is no automatic downgrade.
3. Download its platform archive and `SHA256SUMS` over HTTPS while the old process
   continues serving. Verify SHA-256, reject unexpected archive entries, and run
   a bounded version check on the staged executable.
4. Atomically replace the binary in the same directory. Bee also replaces its
   manifest when installed as a linked plugin. Configuration files are untouched.
5. Ask the existing process to close its connections and execute the new binary.
   Confirm its reported running version through the private local control socket.

This is an online upgrade with a brief reconnect, **not a zero-disconnection hot
update**. Live SSH encryption and Go process state are not transferred. Local
Herdr sessions and agents stay running. Sharing rules, device identities, display
names and share IDs survive. Input buffered only in disconnected transports is
not replayed. Herdr controls consumer reconnection behavior.

## Boundaries

- GitHub release metadata is bounded to 1,000 entries, with at most 4 MiB per page;
  archives are capped at 64 MiB. Downloads have a total three-minute deadline.
  Checksums share the release's GitHub trust boundary; they are integrity checks,
  not independently signed provenance.
- The binary is atomically replaced. Bee's manifest is a separate atomic file
  replacement, not a filesystem transaction with the binary. An interruption
  between replacements may leave stale manifest metadata; executable behavior and
  publisher status remain the authority. Reinstall the same release archive to
  repair such an interrupted installation.
- File replacement does not grant filesystem privileges. Hive's restricted daemon
  stays restricted; an administrator updates its binary from outside the service.
  Normal Bee updates need no elevated permission in a user-owned plugin directory.
- Download, checksum and staged binary validation failures leave the running
  process untouched. If installation succeeds but activation fails, the command
  reports an error containing the installed version. Repeating the update can
  retry activation; inspect the service rather than assuming a successful switch.
- Config schema version 1 is unchanged. Protocol v1 is unchanged. Unknown future
  configuration versions remain rejected. Each future schema change must ship
  its forward migration and compatibility tests with that release.
- No automatic update timer, persistent update cache, version history, rollback
  command or additional background service is installed. Temporary downloads and
  staging files are removed at command completion. The per-installation lock file
  is empty and may remain for safe lock reuse.

[GitHub release API](https://docs.github.com/en/rest/releases/releases) is used for
component discovery. Process replacement uses the platform's
[exec operation](https://go.dev/src/syscall/exec_unix.go).
