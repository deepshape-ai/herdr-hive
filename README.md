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
test -e authorized_tokens || printf '[]\n' > authorized_tokens
./hive --listen 0.0.0.0:2222 \
  --state-dir "$PWD/state" \
  --authorized-keys "$PWD/authorized_keys" \
  --authorized-tokens "$PWD/authorized_tokens"
```

Keep Hive running and make port 2222 reachable. In another terminal, create an
invitation using the address members can reach:

```sh
cd "$HOME/.local/share/herdr-hive"
./hive enroll issue --tokens ./authorized_tokens --state-dir ./state \
  --hive hive.example.internal:2222 --label onboarding
```

Send the JSON `invitation` value (starting with `hinv1-`) through a trusted channel.
It packages the address, public host key and enrollment token into one paste.
Default invitations never expire and allow unlimited devices; `--ttl` and
`--max-uses` retain their existing meanings. [Service setup and revocation](hive/README.md).

### 2. Install Bee inside Herdr

On each new device:

```sh
curl -fsSL https://raw.githubusercontent.com/deepshape-ai/herdr-hive/main/install.sh | sh -s -- bee
```

Bee is installed into `~/.local/share/herdr-bee` and linked to Herdr automatically.
The commands below use its full path; no PATH changes are needed.

Alternatively, install from source with Herdr's GitHub installer (Git and Go
**1.27.1+** required):

```sh
herdr plugin install deepshape-ai/herdr-hive/bee/plugin
herdr plugin action invoke configure --plugin herdr.bee
```

Use the Bee panel to join in step 3. See the
[plugin installation guide](bee/plugin/README.md) for updates and removal.

### 3. Join with an invitation

Open Bee from Herdr's plugin actions, or run:

```sh
herdr plugin action invoke configure --plugin herdr.bee
```

Paste the administrator's invitation into **Invitation** and click **Join Hive**.
Bee creates a private device key, verifies Hive using the invitation's public key,
registers the device, and adds **Hive** to the Herdr sidebar. No manual SSH files
or machine command are needed. The invitation is masked and is not saved.

If native setup is interrupted after registration, click **Finish connection**;
no new invitation is needed. Existing manually configured devices can use the
same button. Joining does not turn on sharing.

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

Choose **Hive** in Herdr's sidebar. Joining in step 3 already added this machine.
Workspaces appear as `[Bee name/session] workspace`; the default session is
shortened to `[Bee name] workspace`. Your own device's publications are hidden.
No other online shares is a valid empty Hive, not a failed join. Devices that only
view others can skip step 4. [Gateway limits](hive/README.md#native-consumer-setup).

<details>
<summary>Advanced: existing keys and raw enrollment tokens</summary>

### Manual registration (advanced)

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
saved; later connections use the device key. This advanced path requires the device key and verified host entry. Run
`bee join` afterwards to add the native receiving machine automatically.


</details>

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
