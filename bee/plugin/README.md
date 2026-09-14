# Herdr Bee

Share selected Herdr named sessions with your team through a Hive server.
Agents keep running on their owners' machines; teammates access shared workspaces
through one native Herdr machine. Bee is the publishing plugin; Hive is the
separately installed server. This is an independent community project.

## Install from GitHub

Requires Herdr **0.9.0+**, Git, Go **1.27.1+**, and OpenSSH on macOS or Linux.
Herdr builds Bee from the checked-out source before registering the plugin:

```sh
herdr plugin install deepshape-ai/herdr-hive/bee/plugin
herdr plugin action invoke configure --plugin herdr.bee
```

Paste the administrator's invitation into the Connection panel and click Join Hive.
Bee handles device registration, SSH configuration and the native Hive machine.
Then select running named sessions in Sharing and start sharing. Follow the
[join guide](../../README.md#3-join-with-an-invitation). Installing Bee alone does not deploy Hive.

For prebuilt binaries without Go, use the [release installer](../../README.md#2-install-bee-inside-herdr).
It links a local plugin instead of creating a GitHub-managed installation.
Choose one installation method; Herdr will not overwrite a linked plugin with
a managed installation.

## Update or remove

Re-run the same `herdr plugin install` command to refresh a GitHub-managed
installation. Plugin configuration is stored separately and survives reinstall.
Stop sharing before removing the plugin:

```sh
herdr plugin action invoke disable --plugin herdr.bee
herdr plugin uninstall herdr.bee
```

Sharing grants registered Hive members **full control of the selected session**,
including all its workspaces, panes and agents. It is not read-only or a sandbox.
Read the [security boundaries](../../SECURITY.md) before sharing.

[Project overview and Hive setup](../../README.md) · [Bee reference](../README.md)
