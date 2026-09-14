# Herdr Hive

![Herdr Hive — share selected sessions with your team while agents keep running on their owners' hosts.](docs/assets/herdr-hive-banner.png)

[English](README.md) | [简体中文](README.zh.md)

Share Herdr sessions with your team. Agents keep running on their owners' hosts;
other members access shared workspaces through one native Herdr machine.

- **Hive** runs on a shared server and connects registered devices.
- **Bee** is a Herdr plugin for joining Hive and choosing sessions to share.

No Herdr source changes or SSH login to members' hosts are needed. Tested with
Herdr **0.9.0**. This is an independent community project.

**Sharing gives registered Hive members full control of the selected session,
including all its workspaces, panes and agents.** See [security boundaries](SECURITY.md).

## Quickstart

Members need [Herdr](https://herdr.dev/) and OpenSSH. The installer selects the
latest stable release for macOS/Linux, amd64/arm64, and verifies its checksum.

### 1. Install Hive on the shared machine

Administrator only. If Hive is already running, start at step 2.

```sh
curl -fsSL https://raw.githubusercontent.com/deepshape-ai/herdr-hive/main/install.sh | sh -s -- hive
cd "$HOME/.local/share/herdr-hive"
touch authorized_keys
./hive enroll issue --tokens ./authorized_tokens --label onboarding
./hive --listen 0.0.0.0:2222 \
  --state-dir "$PWD/state" \
  --authorized-keys "$PWD/authorized_keys" \
  --authorized-tokens "$PWD/authorized_tokens"
```

`enroll issue` prints a JSON `secret` beginning with `hreg-`. By default it never
expires and has unlimited uses. Keep Hive running and make port 2222 reachable.

Give members the Hive address, token and verified `known_hosts` entry through a
trusted channel. See [how to export the host entry](hive/README.md#connection-details-for-new-members).
Device public keys are registered automatically when members redeem the token.
[Service setup, token limits and revocation](hive/README.md).

### 2. Install Bee inside Herdr

On each new device:

```sh
curl -fsSL https://raw.githubusercontent.com/deepshape-ai/herdr-hive/main/install.sh | sh -s -- bee
```

Bee is installed into `~/.local/share/herdr-bee` and linked to Herdr automatically.
The commands below use its full path; no PATH changes are needed.

### 3. Register with a token

First create a dedicated device key, unless you already have one:

```sh
mkdir -p "$HOME/.ssh"
ssh-keygen -t ed25519 -f "$HOME/.ssh/hive_device"
```

If you set a key passphrase, run `ssh-add "$HOME/.ssh/hive_device"` to unlock it.
Save the administrator's verified host entry to `~/.ssh/hive_known_hosts`.
This must be a `known_hosts` entry containing the address, key type and public key,
not just a fingerprint.

Replace the example address and token with the administrator's values:

```sh
"$HOME/.local/share/herdr-bee/bee" configure \
  --hive hive.example.internal:2222 \
  --token hreg-REPLACE_WITH_YOUR_TOKEN \
  --identity "$HOME/.ssh/hive_device" \
  --known-hosts "$HOME/.ssh/hive_known_hosts"
```

Success registers the device and saves its connection settings. The token is not
saved; later connections use the device key. **The current Bee still requires
this key and a verified host entry. Address and token alone are not enough.**

### 4. Choose sessions and start sharing

```sh
herdr plugin action invoke configure --plugin herdr.bee
```

1. In **[1] Connection**, optionally change your visible name and save with Ctrl+S.
2. In **[2] Sharing**, select an existing running named session, such as `default`.
3. Choose **Start sharing**. Confirm the header shows **Connected**.

A named session contains its own workspaces, panes and agents. Closing the Bee
panel leaves sharing running. For scripts, see the [Bee CLI steps](bee/README.md#first-publication).

### 5. View other members' sharing

After registering this device in step 3, add the following to `~/.ssh/config`,
replacing the example host with your Hive host:

```sshconfig
Host hive
    HostName hive.example.internal
    Port 2222
    User hive
    IdentityFile ~/.ssh/hive_device
    UserKnownHostsFile ~/.ssh/hive_known_hosts
    IdentitiesOnly yes
    StrictHostKeyChecking yes
```

```sh
herdr machine add hive --label Hive
```

Herdr automatically shows shared workspaces as `[Bee name] session / workspace`,
for example `[Alice] research / xxx`. Using the same device key hides your own
sharing. A device that only views others can skip step 4.
[Gateway limits and direct-session access](hive/README.md#native-consumer-setup).

## Update

On a Bee host:

```sh
"$HOME/.local/share/herdr-bee/bee" update
```

On the Hive server, in another terminal:

```sh
"$HOME/.local/share/herdr-hive/hive" update \
  --state-dir "$HOME/.local/share/herdr-hive/state"
```

For systemd installations, use `sudo /usr/local/bin/hive update --state-dir /var/lib/herdr-hive`.
Updates briefly disconnect remote viewers; publishers reconnect and local agents
keep running. Use these commands for upgrades; the installer is for first-time
setup. [Update details](docs/UPDATING.md).

## Development and documentation

```sh
make build
make test vet
```

- [Hive deployment, tokens and inspection](hive/README.md)
- [Bee CLI and panel controls](bee/README.md)
- [Architecture and compatibility](docs/DESIGN.md)
- [Verification evidence](docs/EVIDENCE.md) and [integration tests](tests/integration/README.md)
- [Packaging and independent releases](docs/RELEASING.md)
- [Contributing](CONTRIBUTING.md)

MIT licensed. Herdr is installed separately under its own license.
