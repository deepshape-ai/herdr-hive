# Herdr Hive

![Herdr Hive：与团队共享选定会话，Agent 继续在各自所有者的主机上运行。](docs/assets/herdr-hive-banner.png)

[English](README.md) | [简体中文](README.zh.md)

与团队共享 Herdr 会话。Agent 继续在所有者的主机上运行，其他成员通过一个原生 Herdr machine 访问共享工作区。

- **Hive** 安装在公共服务器上，连接已注册的设备。
- **Bee** 是 Herdr 插件，用于接入 Hive、选择要共享的会话。

无需修改 Herdr 源码，也无需登录成员主机的 SSH 账户。已验证 Herdr **0.9.0**，本项目由社区独立开发。

**共享会将所选会话的完整操作权限授予已注册的 Hive 成员，包括其中所有工作区、pane 和 agent。** 详见[安全边界](SECURITY.md)。

## 快速开始

成员主机需安装 [Herdr](https://herdr.dev/) 和 OpenSSH。安装脚本自动选择 macOS/Linux、amd64/arm64 对应的最新稳定版，并校验安装包。

### 1. 在公共机器安装 Hive

由管理员执行。如果已经有可用的 Hive，从第 2 步开始。

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

保持 Hive 运行，并让成员能够访问端口 2222。在另一个终端生成邀请，地址填写成员能访问的地址：

```sh
cd "$HOME/.local/share/herdr-hive"
./hive enroll issue --tokens ./authorized_tokens --state-dir ./state \
  --hive hive.example.internal:2222 --label onboarding
```

通过可信渠道向成员发送输出中 `invitation` 的值（以 `hinv1-` 开头）。它已经包含地址、主机公钥和注册 token，成员只需粘贴一次。默认无限期、不限设备数；仍可通过 `--ttl`、`--max-uses` 设置限额。[常驻服务与邀请撤销](hive/README.md)。

### 2. 在 Herdr 中安装 Bee

在每台新设备上执行：

```sh
curl -fsSL https://raw.githubusercontent.com/deepshape-ai/herdr-hive/main/install.sh | sh -s -- bee
```

Bee 安装到 `~/.local/share/herdr-bee`，并自动注册到 Herdr。下面使用可执行文件的完整路径，无需修改 PATH。

也可以通过 Herdr 的 GitHub 安装器从源码安装（需要 Git 和 Go **1.27.1+**）：

```sh
herdr plugin install deepshape-ai/herdr-hive/bee/plugin
herdr plugin action invoke configure --plugin herdr.bee
```

安装后按第 3 步在 Bee 面板加入 Hive。
更新与卸载见[插件安装指南](bee/plugin/README.md)。

### 3. 粘贴邀请，加入 Hive

从 Herdr 的插件操作打开 Bee，或执行：

```sh
herdr plugin action invoke configure --plugin herdr.bee
```

把管理员提供的邀请码粘贴到 **Invitation**，点击 **Join Hive**。
Bee 自动生成专用设备密钥、核验 Hive、注册设备，并将 **Hive** 添加到 Herdr 侧栏，无需手写 SSH 配置。邀请码以掩码显示，注册后不保存。

如果注册后连接步骤中断，点击 **Finish connection** 即可继续，无需新邀请。已经手动配置过 Bee 的设备也可以点击这个按钮补齐接收端。加入 Hive 不会自动开启自己的共享。

### 4. 选择会话并开始共享

```sh
herdr plugin action invoke configure --plugin herdr.bee
```

1. 在 **[1] Connection** 中按需修改可见名称，Ctrl+S 保存。
2. 在 **[2] Sharing** 中选择已经运行的 named session，例如 `default`。
3. 选择 **Start sharing**，确认顶部显示 **Connected**。

一个 named session 包含自己的工作区、pane 和 agent。关闭 Bee 面板不会停止共享。脚本接入见 [Bee CLI 步骤](bee/README.md#first-publication)。

### 5. 访问其他成员的共享

在 Herdr 侧栏选择 **Hive**。第 3 步已经完成接收端配置，无需再执行 `machine add`。

共享工作区显示为 `[Bee name/session] workspace`；默认 session 简写为 `[Bee name] workspace`。自己的共享会自动隐藏。暂时没有其他在线共享时，Hive 为空是正常状态，不是接入失败。只查看他人共享的设备可以跳过第 4 步。[聚合限制和单会话直连](hive/README.md#native-consumer-setup)。

<details>
<summary>高级用法：已有密钥与原始注册 token</summary>

### 手动注册（高级用法）

如果还没有专用设备密钥，先创建：

```sh
mkdir -p "$HOME/.ssh"
ssh-keygen -t ed25519 -f "$HOME/.ssh/hive_device"
```

如果设置了密钥密码，执行 `ssh-add "$HOME/.ssh/hive_device"` 解锁。将管理员提供的已核验主机公钥条目保存到 `~/.ssh/hive_known_hosts`。文件需要包含地址、密钥类型和公钥，不能只填写指纹。

把示例地址和 token 替换为管理员提供的值：

```sh
"$HOME/.local/share/herdr-bee/bee" configure \
  --hive hive.example.internal:2222 \
  --token hreg-REPLACE_WITH_YOUR_TOKEN \
  --identity "$HOME/.ssh/hive_device" \
  --known-hosts "$HOME/.ssh/hive_known_hosts"
```

成功后，设备完成注册并保存连接设置。token 不会保存，后续连接使用设备密钥。这条高级路径需要手工准备密钥和已核验的主机公钥条目。随后执行 `bee join`，即可自动添加 Herdr 接收端 machine。


</details>

## 更新

在 Bee 主机上执行：

```sh
"$HOME/.local/share/herdr-bee/bee" update
```

在 Hive 服务器的另一个终端执行：

```sh
"$HOME/.local/share/herdr-hive/hive" update \
  --state-dir "$HOME/.local/share/herdr-hive/state"
```

systemd 安装方式使用 `sudo /usr/local/bin/hive update --state-dir /var/lib/herdr-hive`。更新时远程访问会短暂断开，分享端自动重连，本地 agent 继续运行。升级使用这些命令，安装脚本用于首次安装。[更新详情](docs/UPDATING.md)。

## 开发与文档

```sh
make build
make test vet
```

- [Hive 部署、令牌和运行详情](hive/README.md)
- [Bee CLI 和面板操作](bee/README.md)
- [架构与兼容性](docs/DESIGN.md)
- [验证记录](docs/EVIDENCE.md)和[集成测试](tests/integration/README.md)
- [打包与独立发版](docs/RELEASING.md)
- [参与开发](CONTRIBUTING.md)

本项目采用 MIT 许可证。Herdr 需另行安装，并遵守其自身许可证。
