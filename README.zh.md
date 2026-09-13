# Herdr Hive

[English](README.md) | [简体中文](README.zh.md)

通过公共 SSH 中继共享指定的 Herdr 会话。Agent 继续在所有者的主机上运行，访问者通过原生 `herdr machine add` 接入。

| 产物 | 安装者 | 职责 |
| --- | --- | --- |
| [Hive](hive/README.md) | 中继管理员 | 设备认证、名称管理、原生 SSH 接入、双向转发和运行详情 |
| [Bee](bee/README.md) | 会话分享者 | Herdr 插件，提供能力一致的 TUI 和 CLI：配置 Hive、设置名称、选择会话、开关共享 |

访问者只需 Herdr 和 OpenSSH。无需消费端插件、个人主机 SSH 登录或 SSH agent 转发，也不修改 Herdr 本身。每台主机都可以分享和访问；使用相同设备身份时，Hive 会隐藏自己的共享并拒绝连接自己的会话。

**共享会将整个 Herdr named session 的完整操作权限授予已注册的 Hive 成员。** 一个 named session 可以包含多个 workspace、pane 和 agent。Hive 受信任，可以接触转发中的内容，默认不记录终端内容。启用前请了解[安全边界](SECURITY.md)。

## 当前状态

首版实现，以原生 Herdr **0.9.0** 为已验证的集成版本。接入兼容范围明确，未知请求会被拒绝。本项目由社区独立开发，并非 Herdr 官方产品。

## 快速开始

分享端和访问端先安装 [Herdr](https://herdr.dev/)。安装脚本使用 macOS 和 Linux 常见的 **sh、curl、tar 和 SHA-256 工具**，无需额外语言运行时。脚本自动选择对应组件的最新稳定版，识别系统及 amd64 / arm64 架构并校验安装包，无需填写版本号或选择平台。

### 1. 在公共机器安装 Hive

```sh
curl -fsSL https://raw.githubusercontent.com/deepshape-ai/herdr-hive/main/install.sh | sh -s -- hive
```

安装位置为 `~/.local/share/herdr-hive`，脚本会创建私有的 `state` 目录。

在安装目录创建 `authorized_keys`，每行放一台参与设备的专用 SSH **公钥**。设备密钥的创建方式见下一节；管理员只收集公钥，不需要私钥或员工主机的 SSH 登录权限。

启动 Hive：

```sh
cd "$HOME/.local/share/herdr-hive"
./hive --listen 0.0.0.0:2222 \
  --state-dir "$PWD/state" --authorized-keys "$PWD/authorized_keys"
```

保持进程运行，让办公内网能够访问端口 2222，并通过可信渠道将启动时打印的主机密钥指纹告知成员。Linux 常驻服务和资源限制配置见 [systemd 部署说明](hive/README.md#start)。

### 2. 在 Herdr 中安装 Bee

```sh
curl -fsSL https://raw.githubusercontent.com/deepshape-ai/herdr-hive/main/install.sh | sh -s -- bee
herdr plugin action invoke configure --plugin herdr.bee
```

安装位置为 `~/.local/share/herdr-bee`，脚本会自动将插件注册到 Herdr 并启用。可执行文件保留在插件目录，无需全局 `bee` 命令或修改 PATH。

如果还没有专用设备密钥，先创建：

```sh
mkdir -p "$HOME/.ssh"
ssh-keygen -t ed25519 -f "$HOME/.ssh/hive_device"
# 如果设置了密钥密码，先解锁供 Bee 使用：
ssh-add "$HOME/.ssh/hive_device"
```

把 `hive_device.pub` 交给 Hive 管理员。获取 Hive 的主机公钥，与管理员提供的指纹核对后加入 `known_hosts` 文件。`ssh-keyscan -p 2222 hive.example.internal` 可以获取候选公钥，但扫描本身不能验证服务器身份。

Bee 会打开独立的分栏 pane。同一操作会聚焦当前 tab 已有的面板；在面板内再次触发则关闭。添加[快捷键配置](bee/README.md#panel-controls)后，可用 `prefix+alt+b` 操作。

1. 在 **Connection** 中填写 Hive 地址、可见名称和设备密钥路径。点击字段或按 Enter 编辑，Ctrl+S 保存。
2. 按 `2` 或点击 **Sessions** 标签，使用方向键和空格，或点击列表，选择已存在且正在运行的 named session。
3. 选择 **Start sharing** 或按 `s` 开启共享。顶部状态自动刷新，不会打断表单输入。

共享范围包含该 named session 内的全部 workspace、pane 和 agent，已注册的 Hive 成员拥有完整控制权。关闭设置面板不会停止共享。[插件自带的 CLI](bee/README.md#first-publication) 提供相同能力，脚本和 agent 可以通过插件目录下的完整路径调用。

### 3. 从另一台主机访问

访问端无需安装 Bee。向 Hive 注册专用设备公钥后，按[原生访问端配置](hive/README.md#native-consumer-setup)查询共享并为选定会话创建 SSH 别名，然后运行：

```sh
herdr machine add hive-colleague --label "Colleague / project"
```

该配置指向选定的共享会话，无需添加 `--remote-session`。分享和访问使用同一设备密钥，才能排除自己发布的会话。

### 4. 更新 Hive 和 Bee

**Bee**：在 Herdr 设置面板选择 **Update Bee**，或运行：

```sh
herdr plugin action invoke update --plugin herdr.bee
# 也可以直接调用插件程序，等待并查看更新结果：
"$HOME/.local/share/herdr-bee/bee" update
```

**Hive**：在中继机器的另一个终端运行：

```sh
"$HOME/.local/share/herdr-hive/hive" update \
  --state-dir "$HOME/.local/share/herdr-hive/state"
```

使用仓库中的 systemd 配置时，对应命令为 `sudo /usr/local/bin/hive update --state-dir /var/lib/herdr-hive`。

两者独立选择各自的最新稳定版，校验下载并启动新程序。下载期间共享保持在线，切换程序时远程连接会短暂断开，分享端自动重连。本地 agent、配置和共享 ID 保留；这不是零断线热更新。

安装脚本用于首次安装，不覆盖已有目录；升级请使用上面的更新命令。如需自定义安装位置，在安装命令末尾添加 `--install-dir /absolute/path`。详见[更新机制](docs/UPDATING.md)。

## 从源码构建

Go 版本和依赖分别固定在 `hive/go.mod` 与 `bee/go.mod` 中。使用发布包不需要安装 Go。

```sh
make build
make test vet
make package VERSION=0.1.0
# 独立打包：
make package-hive VERSION=0.1.0
make package-bee VERSION=0.1.0
```

也可以独立构建组件：

```sh
cd hive && go build ./cmd/hive
# 或从仓库根目录运行：
cd bee && go build ./cmd/bee
```

安装包生成在 Git 忽略的 `dist/` 目录。Hive 和 Bee 分别使用 `hive/vX.Y.Z`、`bee/vX.Y.Z` 标签发版，通过[协议 v1](protocol/v1.md)通信。上述构建命令不会创建远程仓库或公开发布版本。

## 文档

- [Hive 部署和运行详情](hive/README.md)
- [Bee 安装与使用](bee/README.md)
- [架构与兼容性](docs/DESIGN.md)
- [一条命令升级](docs/UPDATING.md)
- [验证证据与限制](docs/EVIDENCE.md)
- [集成测试说明](tests/integration/README.md)
- [GitHub Actions 发版](docs/RELEASING.md)
- [参与开发](CONTRIBUTING.md)

本项目采用 MIT 许可证。Herdr 需另行安装，并遵守其自身许可证。
