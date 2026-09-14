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
./hive enroll issue --tokens ./authorized_tokens --label onboarding
./hive --listen 0.0.0.0:2222 \
  --state-dir "$PWD/state" \
  --authorized-keys "$PWD/authorized_keys" \
  --authorized-tokens "$PWD/authorized_tokens"
```

`enroll issue` 输出 JSON，其中 `secret` 是以 `hreg-` 开头的 token，默认无限期、不限使用次数。保持 Hive 进程运行，并让成员能够访问端口 2222。

通过可信渠道向成员提供 Hive 地址、token 和已核验的 `known_hosts` 条目，具体见[导出主机公钥条目](hive/README.md#connection-details-for-new-members)。成员使用 token 接入时，设备公钥会自动注册。[常驻服务、令牌限额与撤销](hive/README.md)。

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

采用此托管安装方式时，在第 3 步准备好密钥和主机公钥条目后，通过 Bee 面板完成连接配置。
更新与卸载见[插件安装指南](bee/plugin/README.md)。

### 3. 使用 token 注册

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

成功后，设备完成注册并保存连接设置。token 不会保存，后续连接使用设备密钥。**当前 Bee 仍需手工准备密钥和已核验的主机公钥条目，仅有地址和 token 还不够。**

### 4. 选择会话并开始共享

```sh
herdr plugin action invoke configure --plugin herdr.bee
```

1. 在 **[1] Connection** 中按需修改可见名称，Ctrl+S 保存。
2. 在 **[2] Sharing** 中选择已经运行的 named session，例如 `default`。
3. 选择 **Start sharing**，确认顶部显示 **Connected**。

一个 named session 包含自己的工作区、pane 和 agent。关闭 Bee 面板不会停止共享。脚本接入见 [Bee CLI 步骤](bee/README.md#first-publication)。

### 5. 访问其他成员的共享

**每台需要查看共享工作区的设备都要执行这一步，包括已经通过 Bee 分享的设备。**
Bee 显示 Connected 只说明发布端已连接 Hive，不会自动给 Herdr 添加接收端 machine。

本机完成第 3 步注册后，将下面的配置加入 `~/.ssh/config`，把示例主机名替换为你的 Hive 主机名。
使用与 Bee 相同的设备密钥和已核验的主机公钥文件，并把此配置块放在通用 `Host *` 默认配置之前：

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
ssh -o BatchMode=yes hive 'list --json'
herdr machine add hive --label Hive
herdr machine list
```

第一条命令会列出其他设备的在线共享，并验证 SSH 身份和连接。`machine add` 提示远端服务就绪后，
已打开的 Herdr 客户端会自动连接，在侧栏找到 **Hive** machine 即可。每台接收设备只需添加一次。

Herdr 会自动展示共享工作区，名称为 `[Bee name] session / workspace`，例如 `[Alice] research / xxx`。使用同一设备密钥会隐藏自己发布的共享。只访问他人共享的设备可以跳过第 4 步。[聚合限制和单会话直连](hive/README.md#native-consumer-setup)。

如果两台 Bee 都显示 Connected，但 Herdr 看不到共享，先检查 `herdr machine list`。
列表为空说明尚未添加接收端。`hive inspect` 中共享会话的 `connections=0` 表示当前没有消费者访问，
不表示 Bee 发布失败。若 `ssh hive 'list --json'` 返回空列表，说明当前没有其他设备发布可见会话；
自己发布的会话会按设计隐藏。

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
