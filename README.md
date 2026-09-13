# Herdr Hive

[English](README.md) | [简体中文](README.zh.md)

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

## Quickstart

Install [Herdr](https://herdr.dev/) on publishing and consuming hosts.
The installer uses **sh, curl, tar and SHA-256 tools** already available on most
macOS and Linux hosts. No additional language runtime is required.
It selects the component's newest stable release, detects macOS/Linux and
amd64/arm64, and verifies the release checksum. No version or platform selection
is needed. Install Hive once on the shared machine and Bee on publishing hosts.

### 1. Install Hive on the shared machine

```sh
curl -fsSL https://raw.githubusercontent.com/deepshape-ai/herdr-hive/main/install.sh | sh -s -- hive
```

Installs into `~/.local/share/herdr-hive` and creates its private `state` directory.

Create `authorized_keys` in this directory with the dedicated **public** keys of
participating devices, one per line. The next section shows how each device
creates its key. Never collect private keys or grant host SSH logins.

Start Hive in the foreground:

```sh
cd "$HOME/.local/share/herdr-hive"
./hive --listen 0.0.0.0:2222 \
  --state-dir "$PWD/state" --authorized-keys "$PWD/authorized_keys"
```

Keep this process running. Make port 2222 reachable from the office network and
share the printed host-key fingerprint with members through a trusted channel.
For a managed Linux service with resource limits, use the
[systemd deployment instructions](hive/README.md#start).

### 2. Install Bee inside Herdr

```sh
curl -fsSL https://raw.githubusercontent.com/deepshape-ai/herdr-hive/main/install.sh | sh -s -- bee
herdr plugin pane open --plugin herdr.bee --entrypoint settings
```

Installs into `~/.local/share/herdr-bee` and automatically links and enables the
Herdr plugin. The executable stays in that directory; no global `bee` command
or PATH change is required.

Create a dedicated device key if you do not already have one:

```sh
mkdir -p "$HOME/.ssh"
ssh-keygen -t ed25519 -f "$HOME/.ssh/hive_device"
# If you chose a passphrase, unlock the key for Bee:
ssh-add "$HOME/.ssh/hive_device"
```

Give `hive_device.pub` to the Hive administrator. Obtain Hive's SSH host key and
verify its fingerprint against the administrator's value before adding it to a
`known_hosts` file. For example, `ssh-keyscan -p 2222 hive.example.internal`
retrieves a candidate key; scanning alone does not verify its identity.

In Bee's settings pane:

1. **Configure Hive:** enter `hive.example.internal:2222`, the absolute path to
   `hive_device`, and the absolute path to your verified `known_hosts` file.
2. **Change visible name:** keep the hostname default or choose your own name.
3. **Select shared sessions:** select an existing, running Herdr named session.
4. **Toggle all sharing:** enable publication and check that `Connected` is true.

A named session includes all its workspaces, panes and agents. Registered Hive
members receive full control of what you publish. Closing the settings pane does
not stop sharing. The [bundled CLI](bee/README.md#first-publication) offers the
same controls for scripts and agents, using its full plugin-directory path.

### 3. Connect from another host

Consumers need no Bee installation. Register their dedicated device public key
with Hive, then follow [native consumer setup](hive/README.md#native-consumer-setup)
to query shares and create an SSH alias for the selected share:

```sh
herdr machine add hive-colleague --label "Colleague / project"
```

The profile points to the selected shared session. Omit `--remote-session` and
use the same device key for publishing and consuming to exclude your own shares.

### 4. Update Hive and Bee

**Bee:** choose **Update Bee** in its Herdr settings pane, or run:

```sh
herdr plugin action invoke update --plugin herdr.bee
# Optional synchronous CLI result, without a global command:
"$HOME/.local/share/herdr-bee/bee" update
```

**Hive:** in another terminal on the relay, use the installation and state
locations from step 1:

```sh
"$HOME/.local/share/herdr-hive/hive" update \
  --state-dir "$HOME/.local/share/herdr-hive/state"
```

For the systemd installation, the equivalent is
`sudo /usr/local/bin/hive update --state-dir /var/lib/herdr-hive`.

Both commands select their component's newest stable release, verify the download
and activate the new program. Downloads keep current sharing online; activation
briefly disconnects viewers and the publishers reconnect. Local agents, settings
and share IDs are preserved. This is **not a zero-disconnection hot update**.

The installer is for new installations and leaves existing directories untouched.
Use the commands above for upgrades. For a custom installation path, append
`--install-dir /absolute/path` to the installer command.
[Update details](docs/UPDATING.md).

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
