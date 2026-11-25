# iadu-sim

iADU 42U 资产条 Modbus 模拟器（Go）。**一份寄存器状态**同时通过：

- **Modbus RTU 从站**（纯 Go pty 虚拟串口，多串口、多从站 ID，供目标采集程序采集）
- **Modbus TCP**（标准 MBAP，0x8000 影子寄存器区，用于调值/验证）
- **内嵌管理控制台**（go:embed + SSE，中文界面，无外部前端依赖）

对外提供服务，寄存器映射与真机抓帧、Python 参考模拟器逐字节对齐。

## 特性

- **42U 资产条寄存器模型**：标签、在位、变化、上/中/下温湿度、总线状态、ESN、LED、设备信息。
- **RTU**：FC01/02/03/04/05/06/0F/10；广播发现（`FF 04 01 50 00 0A`）；多端口、多从站；同一设备可绑定多个端口共享寄存器；宽松地址模式；可选地址协商。
- **Modbus TCP**：标准 MBAP；0x8000 影子区读写；FC01/02/03/04/05/06/0F/10；多客户端；unit 映射与异常码。
- **动态温湿度**：日夜曲线 + OU 平滑噪声 + 三区温差/滞后 + 手动覆盖；`time_scale` 可加速演示。
- **管理页面**：总览、设备详情、U 位/标签、LED、总线、温湿度参数、串口绑定、实时帧日志、设置；可增删设备/绑定并持久化。
- **持久化**：`config.yaml` 管理进程/端口/种子；`state.json` 是设备/绑定的运行时权威，原子写入并保留 `.bak`。
- **打包**：deb（amd64/arm64/armv7）、rpm（amd64/arm64）、tar.gz（amd64/arm64）+ systemd 单元 + GitHub Actions。

## 架构

```
                    ┌──────────────────────────────────────────────┐
 目标采集程序         │                 iadu-sim 进程                │
 (RTU 主站)  ────────┼──▶ pty COM1 ─▶ RTU 解析/路由 ─┐             │
 打开 run/com1        │   pty COM2 ─▶                 │             │
                    │                                ▼             │
 mbpoll / pymodbus ─┼──▶ Modbus TCP (MBAP) ─▶ 0x8000 影子区 ─▶ Hub │
                    │                                │             │
 浏览器 ────────────┼──▶ HTTP API + SSE + 内嵌 UI ───▶│             │
                    │                                ▼             │
                    │                    Device × N（共享寄存器）    │
                    │                    Climate ticker / state.json │
                    └──────────────────────────────────────────────┘
```

## 快速开始

### 构建与自检

```bash
go build -o bin/iadu-sim ./cmd/iadu-sim
./bin/iadu-sim --selftest
```

`--selftest` 校验 CRC/帧/寄存器/影子区；Linux 上还会创建 pty 做读写回环（Windows 会 SKIP）。

### 开发运行

```bash
./bin/iadu-sim --dump-config            # 查看默认配置
./bin/iadu-sim --dir ./.iadu-sim        # 首次运行自动生成配置/状态/日志
```

- 管理页面：`http://127.0.0.1:18090`
- Modbus TCP：`0.0.0.0:15020`
- Linux：`./.iadu-sim/run/com1` 是可供采集程序打开的稳定软链
- Windows：没有 pty，RTU 端口会跳过；TCP 与页面可用于开发调试

### 生产安装

```bash
# deb（Ubuntu 18+）
sudo dpkg -i iadu-sim_<version>_linux_amd64.deb

# rpm（CentOS 7+）
sudo rpm -Uvh iadu-sim-<version>-1.x86_64.rpm

# tar.gz（含 install.sh / uninstall.sh）
tar xzf iadu-sim_<version>_linux_amd64.tar.gz
cd iadu-sim_<version>_linux_amd64 && sudo ./install.sh
```

默认目录 `/opt/apps/iadu-sim/`，服务 `iadu-sim.service` 默认开机自启。详见
[docs/deployment.md](docs/deployment.md)。

> Web 默认只监听 `127.0.0.1:18090`。远程访问请用 `ssh -L 18090:127.0.0.1:18090 user@server`，
> 或改 `server.web.listen` 并设置 `server.web.token`。

## RTU 采集接入

1. 采集程序打开配置中的 `link` 路径，例如 `/opt/apps/iadu-sim/run/com1`。
2. 串口参数按 **9600, 8N1** 设置；从站地址使用 `bindings[].unit_id`（默认 1）。
3. 广播发现帧 `FF 04 01 50 00 0A` 会得到 20 字节 ESN 应答。
4. 常规轮询 10 条帧：ESN、三段标签、在位、变化、LED、温湿度、总线、设备信息。
5. **模拟器重启后 pts 编号会变化**（软链会更新），采集程序必须重新打开。

严格映射与编码见 [docs/register-map.md](docs/register-map.md)。完整逐字节 golden 集在
`internal/modbus/testdata/rtu_golden.json`。

## Modbus TCP 调值

标准 Modbus TCP，影子区从 `0x8000` 开始，与 RTU 原生地址不冲突：

| 地址 | 内容 |
|------|------|
| `0x8000-0x8005` | 设备信息（只读） |
| `0x8006-0x800F` | ESN |
| `0x8010 + (U-1)*4` | U 位标签 |
| `0x80C0-0x80C2` | 温湿度 raw（写入即手动冻结） |
| `0x80C3-0x80C5` | 总线状态 |
| `0x80D0-0x80F9` | LED |
| `0x8100-0x8102` / `0x8110-0x8112` | 在位 / 变化位图 |
| `0x8120` / `0x8121` | 命令 / 手动掩码 |
| 线圈 `0x8000-0x8029` 等 | 在位、变化、手动开关 |

```bash
mbpoll -m tcp -p 15020 -a 1 -0 -r 32768 -c 6 -t 3 127.0.0.1   # 设备信息
mbpoll -m tcp -p 15020 -a 1 -0 -r 32832 -c 3 -t 3 127.0.0.1   # 温湿度
mbpoll -m tcp -p 15020 -a 1 -0 -r 32784 -c 4 -t 3 127.0.0.1   # U1 标签
```

完整地址表与示例见 [docs/adjust-map.md](docs/adjust-map.md)。

## 管理页面

- **总览**：设备卡片、三区温湿度、在位统计、端口与帧统计、趋势曲线。
- **设备详情**：温湿度手动/自动、U 位与标签、总线状态、设备信息、LED、串口绑定、动态温湿度参数、影子映射参考。
- **端口**：稳定软链、pty slave、绑定设备、RX/TX/CRC 统计。
- **实时日志**：RTU/MBAP 帧，按来源/方向过滤、暂停、复制报文。
- **设置**：版本、路径、监听地址、访问令牌、部署说明。
- 页面可增删设备与绑定；拓扑（串口数量/路径）由配置文件定义。

## 配置与状态

- `config.yaml`：进程、端口、首次运行设备/绑定种子。
- `state.json`：首次从 config 播种后成为设备/绑定的权威；页面/RTU/TCP 的变更防抖原子写入，保留 `.bak`。
- 修改 config 的 devices/bindings 后：

```bash
iadu-sim --import-config --config /opt/apps/iadu-sim/config/config.yaml
```

- 状态损坏救援：`--allow-corrupt-state`。
- 详见 [docs/deployment.md](docs/deployment.md#7-配置与状态)。

## CLI

```
iadu-sim [--config PATH] [--dir DIR]
         [--version] [--check-config] [--dump-config] [--selftest]
         [--import-config] [--allow-corrupt-state]
```

| 参数 | 说明 |
|------|------|
| `--config` | 配置文件（默认 `<dir>/config/config.yaml`） |
| `--dir` | 默认根目录（Linux `/opt/apps/iadu-sim`） |
| `--selftest` | 离线协议 + pty 自检 |
| `--check-config` | 校验配置后退出 |
| `--dump-config` | 输出默认配置 |
| `--import-config` | 忽略 state.json，按配置重新播种 |
| `--allow-corrupt-state` | 状态损坏时按配置重新播种 |

## 打包与发布

- `.github/workflows/release.yml`：push tag `v*` 自动创建 GitHub Release；`workflow_dispatch` 手动构建并上传 artifact。
- `.github/workflows/ci.yml`：push/PR 执行 `go vet`、`go test -race`、`--selftest`、GoReleaser snapshot 与 deb 架构检查。
- 本地验证：

```bash
goreleaser check
goreleaser release --snapshot --clean
```

已实测：deb 内部架构 `amd64` / `arm64` / `armhf`；rpm 为 `x86_64` / `aarch64`；二进制自动装入 `/usr/bin`；
tar 包含 `install.sh` / `uninstall.sh` / 服务单元 / 示例配置。

## 测试

```bash
go test -race ./...
go run ./cmd/iadu-sim --selftest
```

- 单元：CRC、帧长/重同步、寄存器、位打包、温湿度量化、配置、state、applog、API。
- Golden：16 条真机/参考实现帧逐字节比对（含广播发现、ESN、三段标签、温湿度、总线、信息、异常）。
- 集成：RTU 字节流、MBAP 多客户端、HTTP CRUD/鉴权/SSE。
- Linux CI 会运行 pty 回环、RTU pty 双端口共享设备端到端、`--selftest` 全链路。
- 核心包测试覆盖率 ≥80%（`go test -cover ./...`）。

## 限制与安全

- pty 只验证协议与业务链，**不验证真实波特率/电气层/时序**；A/B 反接、终端电阻、共地需现场验证。
- 模拟器重启后采集程序必须重新打开 pts；广播读多设备会按顺序拼接应答（可配 `broadcast_mode: first`）。
- Web 默认仅本机；跨机访问务必设置 token 或使用 SSH 隧道。Modbus TCP **无认证**，请用防火墙限制来源。
- pty slave 默认 `0666` 便于同机采集程序打开；可收紧为 `0660` + `dialout` 组。
- 这是测试工具，不应作为生产 Modbus 设备或安全边界使用。

## 文档

- [寄存器映射](docs/register-map.md)
- [Modbus TCP 影子映射](docs/adjust-map.md)
- [部署与运维](docs/deployment.md)

## 许可

内部测试工具，未附带开源许可（Proprietary）。
