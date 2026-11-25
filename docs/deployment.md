# 部署与运维指南

## 1. 交付物与平台

| 包 | 架构 | 目标系统 |
|----|------|----------|
| `.deb` | amd64（x86_64）、arm64、armv7（armhf） | Ubuntu 18.04+ / Debian 系 |
| `.rpm` | amd64（x86_64）、arm64（aarch64） | CentOS 7+ / RHEL 系 |
| `.tar.gz` | amd64、arm64，含 `install.sh` / `uninstall.sh` | 任意 systemd Linux |

- 二进制使用 `CGO_ENABLED=0` 静态编译，目标机不需要 Go、glibc 版本无关。
- 默认端口：Web `127.0.0.1:18090`，Modbus TCP `0.0.0.0:15020`，都 >1024，不需要 root 权限。
- `x86` 按 x86_64/amd64 构建；不提供 32 位 i386。

## 2. 目录布局

```text
/usr/bin/iadu-sim                          # 二进制（包管理器管理）
/usr/share/iadu-sim/config.example.yaml    # 安装时使用的配置模板
/usr/share/iadu-sim/README.md
/opt/apps/iadu-sim/
├── config/config.yaml                     # 配置：root:iadu-sim 0640
├── logs/                                  # 日志（自动轮转），属主 iadu-sim
├── run/                                   # com1 -> /dev/pts/N 稳定软链
├── data/state.json                        # 设备/绑定运行时权威
└── README.md
```

systemd 服务名：`iadu-sim.service`，安装后默认 enable + start。

## 3. 安装

### deb（Ubuntu 18+）

```bash
sudo dpkg -i iadu-sim_<version>_linux_amd64.deb
# 如提示缺少基础依赖（通常没有）：
sudo apt-get -f install
systemctl status iadu-sim
```

### rpm（CentOS 7+）

```bash
sudo rpm -Uvh iadu-sim-<version>-1.x86_64.rpm
# 或
sudo yum localinstall -y iadu-sim-<version>-1.x86_64.rpm
systemctl status iadu-sim
```

### tar.gz

```bash
tar xzf iadu-sim_<version>_linux_amd64.tar.gz
cd iadu-sim_<version>_linux_amd64
sudo ./install.sh              # 或 sudo sh install.sh
# 容器/不希望立即启动：
sudo ./install.sh --no-start
```

tar 安装器会创建 `iadu-sim` 系统用户与目录、安装 `/usr/bin/iadu-sim`、把服务单元放到
`/etc/systemd/system/iadu-sim.service`、enable 并 start。

## 4. 服务与日志

```bash
systemctl status iadu-sim
systemctl restart iadu-sim
journalctl -u iadu-sim -f
tail -f /opt/apps/iadu-sim/logs/iadu-sim.log
```

- 日志同时输出到 stdout（journal）和文件；文件按 `max_size_mb`/`max_backups` 轮转。
- 服务以 `iadu-sim` 用户运行；`ProtectSystem=full`、`ProtectHome=true`、`PrivateTmp=true`。
- 不要给服务加 `PrivateDevices=true`，否则看不到 `/dev/pts`，RTU 端口无法创建。

## 5. 权限与采集程序接入

- pty slave 默认 `0666`，同机任意用户可打开；如要收紧：
  - 配置 `ports[].mode: "0660"`、`ports[].group: "dialout"`；
  - 把采集程序用户加入 `dialout`。
- 如果 `link` 路径已存在一个**普通文件/设备节点**，默认拒绝覆盖（防止误删 `/dev/ttyWCHx`）；
  将 `ports[].replace_existing` 设为 `true` 时会先重命名为 `.bak.<时间戳>` 再创建软链。
- 采集程序打开稳定路径：`/opt/apps/iadu-sim/run/com1`（或配置里 `link` 的值）。
- 串口参数按 **9600, 8N1** 配置；pty 上波特率不生效，但语义保持不变。
- 从站地址由 `bindings[].unit_id` 决定，默认 1；采集程序必须使用相同的地址。
- **模拟器重启后 `/dev/pts/N` 会变化**，软链会更新；采集程序如果只 open 一次，会持有旧 fd，必须重新打开（建议实现断线重连）。
- 多个采集程序不要同时打开同一个 pty（会各读一部分字节）；主备通道请使用两个端口绑定同一设备。

## 6. 网络与访问

### Web 管理页面

默认只监听 `127.0.0.1:18090`（安全默认）。远程访问两种方式：

```bash
# 1) SSH 隧道（推荐）
ssh -L 18090:127.0.0.1:18090 user@server
# 浏览器打开 http://127.0.0.1:18090
```

```yaml
# 2) 局域网直连：改配置并设置 token
server:
  web:
    listen: "0.0.0.0:18090"
    token: "请改成随机长字符串"
```

```bash
systemctl restart iadu-sim
# 浏览器访问 http://server:18090，在页面右上角“访问令牌”填入 token
```

也可以用环境变量 `IADU_SIM_TOKEN` 覆盖配置文件令牌（systemd 可写 Environment=）。

### Modbus TCP

- 默认 `0.0.0.0:15020`；外部调值主站直连。
- 服务没有认证，请通过防火墙限制来源；不要直接暴露到公网。
- 一个设备的 `tcp_unit_id` 唯一；unit 0/255 使用 `default_unit`。

## 7. 配置与状态

- `config.yaml`：进程、端口（串口拓扑）、首次运行的设备/绑定种子。
- `state.json`：第一次启动从 config 播种后即成为设备/绑定的**运行时权威**。
  - 页面上的改值、增删设备/绑定会原子写入 state.json；
  - RTU/TCP 写入的值也会防抖保存（自动温湿度计算值、变化位不保存）；
  - 保留 `state.json.bak`；主文件损坏时自动回退。
- 修改 config 的 `devices`/`bindings` 后需要重新播种：

```bash
# 忽略 state.json，按 config 重新播种（旧 state 会保留为 .bak）
iadu-sim --import-config --config /opt/apps/iadu-sim/config/config.yaml

# state 主文件与备份都损坏时，按 config 重新播种
iadu-sim --allow-corrupt-state --config /opt/apps/iadu-sim/config/config.yaml
```

> 通过 systemd 管理时，建议先停服务再手动执行上述命令，随后 start，避免两个进程同时写状态。

- 修改端口集合/listen 地址/令牌后需要重启服务。
- 日志路径、状态路径可在 config 中改；目录必须对 `iadu-sim` 用户可写。

## 8. 温湿度行为

- 默认按真实时间日夜曲线变化：约 15:00 最高、03:00 最低；三区有温差与滞后；
  湿度与温度反向微调；叠加 OU 平滑噪声。典型值 24–27℃ / 45–55%RH。
- `time_scale` 可加速演示（例如 60 表示一天一分钟）；`update_ms` 控制更新周期。
- 页面“手动设置”或 TCP 影子区写温湿度会冻结该区；到期或“恢复自动”后继续曲线。
- 关闭 `climate.enabled` 后使用 `fixed.temp/humi` 固定值，仍支持手动覆盖。

## 9. 升级与卸载

- deb/rpm 升级会保留 `/opt/apps/iadu-sim/config/config.yaml` 与 `state.json`；
  postinstall 会重启服务。采集程序需要重新打开 pty。
- deb 卸载：`sudo dpkg -r iadu-sim`；rpm：`sudo rpm -e iadu-sim`。数据保留。
- tar 卸载：`sudo ./uninstall.sh`（保留数据）或 `sudo ./uninstall.sh --purge`（删除配置/状态/日志和用户）。

## 10. 排错

| 现象 | 处理 |
|------|------|
| 页面 401 | 服务端设置了 token；在页面右上角填写相同令牌。 |
| 页面打不开 | 默认只监听 127.0.0.1；用 SSH 隧道或改 listen；检查防火墙。 |
| RTU 采集端离线 | 检查 `run/com1` 是否指向有效 pts、采集用户是否有权限、从站地址是否匹配；Windows 无 pty，仅 Linux 支持 RTU。 |
| 重启模拟器后采集端无数据 | 采集程序持有旧 pts fd；重启采集程序或实现重开。 |
| 同一端口多设备发现异常 | 广播读会按绑定顺序拼接多个应答；如主站只接受一个，配置 `broadcast_mode: first`。 |
| Modbus TCP 异常 0x0B | unit 不存在；检查设备的 `tcp_unit_id`。 |
| Modbus TCP 异常 0x02 | 地址低于 0x8000、写只读信息/掩码、或越界。 |
| 状态加载失败 | 用 `--import-config` 或 `--allow-corrupt-state` 重新播种；先备份 state.json。 |
| 服务反复重启 | 看 `journalctl -u iadu-sim`；常见为端口被占用、配置校验失败、目录权限不足。 |
| 确认二进制/协议正常 | `sudo -u iadu-sim /usr/bin/iadu-sim --selftest`（含 pty 回环）。 |

## 11. 兼容性说明

- Ubuntu 18.04 的 systemd 237、CentOS 7 的 systemd 219 均可运行本单元；脚本使用 `enable` + `start`，不依赖 `enable --now`。
- CentOS 7 的 rpm 4.11 对 zstd 载荷不友好；rpm 使用 **gzip** 压缩。
- armv7 的 deb 内部架构为 `armhf`（GoReleaser/nFPM 映射已验证）。
- 静态二进制可在无 Go、glibc 较旧的系统运行；不依赖运行时库。
- 真实 485 的 A/B、终端电阻、共地、时序无法由 pty 验证；现场需另测。

## 12. 验收清单

1. `systemctl status iadu-sim` active；`journalctl` 无持续报错。
2. `ls -l /opt/apps/iadu-sim/run/com1` 指向 `/dev/pts/N`。
3. 采集程序打开 link 后，10 条轮询帧均有应答；设备在线。
4. 页面可改温湿度/标签/在位/LED/总线；采集端下一轮读到新值。
5. `mbpoll` 读 0x8000 影子区与页面一致；TCP 写 LED 后回读一致。
6. 重启服务后 state.json 中的设备/绑定/手动值保留。
7. 重新开机后服务自动启动。
