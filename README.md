# Herdr Hive

Share selected Herdr sessions through a central SSH relay. Keep agents on their
owners' machines and consume them with native `herdr machine add`.

| Product | Installed by | Responsibility |
| --- | --- | --- |
| [Hive](hive/README.md) | Relay administrator | Device authentication, names, native SSH adaptation, bounded duplex forwarding and operational details |
| [Bee](bee/README.md) | Session publisher | Herdr plugin with matching terminal UI and CLI: configure Hive, choose a name, select sessions, toggle sharing |

Consumers need Herdr and OpenSSH. No consumer plugin, personal-host SSH login,
SSH agent forwarding or Herdr source changes are required. Any host can publish
and consume. Hive excludes a device's own shares from its directory and rejects
self-connections when the same device identity is used.

**Sharing grants full control of an entire Herdr named session to registered
Hive members.** A named session can contain multiple workspaces, panes and agents.
Hive is trusted with content in transit and does not record terminal contents.
Read [security boundaries](SECURITY.md) before enabling sharing.

## Status

Initial implementation. Native Herdr **0.9.0** is the tested integration target.
Bootstrap compatibility is explicit and unknown requests fail closed. This is an
independent community project, not an official Herdr product.

## Build

Go versions and dependencies are pinned independently in `hive/go.mod` and
`bee/go.mod`. Production users install binaries; they do not need Go.

```sh
make build
make test vet
make package VERSION=0.1.0
# Independent delivery:
make package-hive VERSION=0.1.0
make package-bee VERSION=0.1.0
```

Each component can also be built independently:

```sh
cd hive && go build ./cmd/hive
# or, from the repository root:
cd bee && go build ./cmd/bee
```

Release archives are generated under ignored `dist/`. Hive and Bee have independent
release tags (`hive/vX.Y.Z`, `bee/vX.Y.Z`) and communicate through [protocol v1](protocol/v1.md).
No remote repository or public release is created by these commands.

## Documentation

- [Hive deployment and inspection](hive/README.md)
- [Bee installation and use](bee/README.md)
- [Architecture and compatibility](docs/DESIGN.md)
- [One-command upgrades](docs/UPDATING.md)
- [Verification evidence and limits](docs/EVIDENCE.md)
- [Integration test instructions](tests/integration/README.md)
- [GitHub Actions releases](docs/RELEASING.md)
- [Contributing](CONTRIBUTING.md)

MIT licensed. Herdr itself is installed separately under its own license.
