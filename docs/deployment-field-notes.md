# iadu-sim 现场部署经验总结（iADU 网关）

> 版本对应：`0.0.0-SNAPSHOT-ae225e6`（linux/armv7，静态）
> 目标设备：`cloud.mrcong.cn:32000` → i.MX6UL / armv7l / Yocto / **sysvinit**
> 记录日期：2026-11-25　|　结果：**8 端口 × 3 台 = 24 台模拟资产条部署成功，com1 已接入并在线（devstate=1）**

本文是"再来一次怎么最快、踩过的坑怎么绕"的操作性总结，不是功能说明。功能与协议见
[README](../README.md)、[register-map](register-map.md)、[adjust-map](adjust-map.md)。

---

## 1. 目标环境画像（先记住这几条，决定后面所有做法）

| 维度 | 实测结论 | 对部署的影响 |
|------|----------|--------------|
| init | `init [5]` → `/sbin/init.sysvinit`；`/etc/init.d` + `rcS.d/rc5.d` + `update-rc.d` | **没有 systemd**，`iadu-sim.service` 不可用，要写 SysV 脚本或 inittab respawn |
| systemd 痕迹 | `/lib/systemd/system/nginx.service` 存在，但无 `systemctl`/`/run/systemd` | 别被这个文件骗了，它只是包残留 |
| pty | 内核支持（`/dev/pts` 已挂 devpts），`sshd` 能分配 | 两条路线都靠它 |
| python | **2.7.11**，但 **缺 `pty` / `tty` 模块**，无 pyserial | 不能在设备上用 py 建 pty 对；探测脚本只能用 `termios` |
| 编译环境 | 无 `gcc/cc/make` | 不能现场编译，必须交叉编译后上传 |
| 桥接工具 | 无 `socat`、`ser2net`、`perl`、`expect`、`screen`、`minicom` | 别指望"装个 socat"；用 sshd pty 或程序自带 pty |
| 常用命令 | `stty/tty/dd/nohup/nc/hexdump` 有；`timeout/setsid` **没有**；`od` 不支持 `-A` | 写脚本时避开这些 |
| busybox `wget` | 对本地 HTTP **不可靠**（连 `127.0.0.1:80` 的 nginx 都返回 rc=1） | **不要用设备上的 wget 判断端口/服务** |
| 日志 | `/var/log` → `/var/volatile/log`（tmpfs） | 重启即丢；长期日志写 `/opt/...` |
| 根文件系统 | UBIFS，361 MB / 已用 146.5 MB / 剩 **209 MB** | 8.3 MB 二进制没问题，但别堆日志 |
| 串口 | `/dev/ttyWCH1`、`/dev/ttyWCH0`（ch432 扩展）+ `/dev/ttymxc2..7` | 8 个口，映射见 §4.2 |

---

## 2. 两条部署路线（按场景选）

| | 路线 A：PC 侧 Python + SSH pty 桥 | 路线 B：网关本地静态二进制（推荐） |
|---|---|---|
| 形态 | `sim_bridge.py` 用 `ssh -tt` 拿远端 pty，串口节点软链过去 | `iadu-sim` 跑在网关上，自己 `posix_openpt` |
| 设备侧安装 | **零安装** | 一个静态二进制 + 配置 |
| 依赖 | PC 的 Python3 + SSH 会话常驻 | 无（纯 Go 静态，不依赖 glibc） |
| 断线影响 | SSH 断 → pty 消失 → 设备离线 | 进程在就在 |
| 可调值 | 改脚本 | Web 页面 / HTTP API / Modbus TCP 影子区 |
| 适合 | 临时验证、设备上什么都不想装 | 长期联调、需要页面与 TCP 调值 |

**结论：长期用 B，临时/无权限用 A。** 本次先 A 验证协议，再 B 落地。

---

## 3. 标准部署流程（路线 B，命令级）

### 3.1 目录约定

```
/opt/iadu-sim/
├── bin/iadu-sim           # 静态二进制（8.3 MB）
├── config/config.yaml     # 拓扑与监听
├── run/com1..com8         # 稳定软链 → /dev/pts/N（进程重启会更新）
├── data/state.json        # 运行时权威状态（删掉即按 config 重新播种）
└── logs/                  # iadu-sim.log + stdout.log（放 /opt，避开 tmpfs）
```

### 3.2 前置检查清单

```sh
df -h / | tail -1                 # 剩余空间 > 20 MB
cat /proc/sys/net/ipv6/bindv6only # 0 → 0.0.0.0 会显示成 tcp6 [::] 但 IPv4 可达
pidof iadu-sim; pidof ioserver    # 记下现状，便于回滚
ls -l /dev/ttyWCH0 /dev/ttyWCH1   # 原节点记下来（major/minor）
```

### 3.3 上传与自检（先不碰现场）

```sh
mkdir -p /opt/iadu-sim/{bin,config,run,data,logs}
# 上传 dist/linux-armv7_linux_arm_7/iadu-sim → /opt/iadu-sim/bin/iadu-sim
chmod +x /opt/iadu-sim/bin/iadu-sim
/opt/iadu-sim/bin/iadu-sim --version
/opt/iadu-sim/bin/iadu-sim --selftest      # 17 项，必须全 PASS（含 pty 回环）
```

> `--selftest` 是唯一能在**不改现场**的情况下确认「ARM 上 pty 能建、协议对」的手段，务必先跑。

### 3.4 配置：生成 → 改 → 校验 → 播种

```sh
iadu-sim --dir /opt/iadu-sim --dump-config > /opt/iadu-sim/config/config.yaml
# 按需改监听（0.0.0.0）或端口/设备
iadu-sim --dir /opt/iadu-sim --check-config     # 期望：N 个端口，M 个设备，K 条绑定
```

**关键：`state.json` 是运行时权威。** 改了 config 里的 `ports/devices/bindings` 后，
必须 **删 `state.json`** 或 `--import-config`，否则改动不生效（这是最容易"改了没反应"的坑）。

### 3.5 启动

```sh
cd /opt/iadu-sim
nohup ./bin/iadu-sim --dir /opt/iadu-sim >> logs/stdout.log 2>&1 &
sleep 3
pidof iadu-sim
netstat -lntp | grep -E "18090|15020"
ls -l run/
```

启动日志应出现：`RTU 端口已就绪 ... pts=/dev/pts/N` × 端口数、`管理页面已就绪`、`Modbus TCP ... 已就绪`。

### 3.6 接入采集（最容易出错的环节）

```sh
# 1) 备份真节点
mv /dev/ttyWCH1 /dev/ttyWCH1.real
# 2) 链到"稳定软链"（不要直接链 pts，否则模拟器重启就断了）
ln -s /opt/iadu-sim/run/com1 /dev/ttyWCH1
# 3) 必须重启 ioserver（它启动时已 open 老节点，换链不重启无效）
cd /opt/iadu && killall -9 ioserver && ./runios.sh
```

**顺序不能反**：模拟器先起（`run/com1` 出现）→ 再换链 → 再重启采集。
回滚同理反序：

```sh
rm /dev/ttyWCH1 && mv /dev/ttyWCH1.real /dev/ttyWCH1
cd /opt/iadu && killall -9 ioserver && ./runios.sh
```

### 3.7 验证（三个维度都要看）

```sh
# ① 网关侧（页面同源 API）
GET /cgi-bin/i_free.cgic?module=device_manage&op=query
#   期望 devstate=1、existucount=42、topTH/midTH/buttonTH 有值、u_status[].tag 正常

# ② 模拟器侧
GET http://127.0.0.1:18090/api/ports
#   期望对应端口 connected=true、rx/tx 持续增长、crc_errors=0

# ③ 帧级
GET http://127.0.0.1:18090/api/frames?limit=10&port=COM1
#   能看到 01 04 01 00 00 03 / 01 04 01 03 00 03 之类的真实收发
```

### 3.8 回滚到"真设备"

见 §3.6 的反序命令；确认 `devstate` 回到 `0`、`/dev/ttyWCH1` 是字符设备 `246,1`、无 `.real` 残留。

---

## 4. 多端口 / 多设备拓扑

### 4.1 8 端口 × 每口 3 台（本次落地）

| 项 | 做法 |
|----|------|
| 端口 | `COM1..COM8`，`transport: pty`，`link: /opt/iadu-sim/run/comN` |
| 设备 | `iadu-{口}-{条}`，每口 `unit_id = 1/2/3` |
| ESN | `E201` + 14 个 0 + 两位序号（20 字符，1..24 唯一） |
| 标签 | 种子 `E2{序号:02X}` + 12 个 0（16 hex = 8 字节） |
| U 位占用 | 三台错开：`1-10,20` / `1-15` / `5-12,30` |
| TCP unit | 1..24 唯一（`tcp_unit_id`） |

配置量很大（~1700 行 YAML），**用脚本生成**（`gen_config.py`），别手写。

### 4.2 网关 com ↔ tty 映射（已确认，接线必须按此）

ioserver 固定打开 8 个节点，映射是**奇偶成对交换**的：

| 通道 | 节点 | 通道 | 节点 |
|------|------|------|------|
| com1 | `/dev/ttyWCH1` | com2 | `/dev/ttyWCH0` |
| com3 | `/dev/ttymxc3` | com4 | `/dev/ttymxc2` |
| com5 | `/dev/ttymxc5` | com6 | `/dev/ttymxc4` |
| com7 | `/dev/ttymxc7` | com8 | `/dev/ttymxc6` |

即 `comN` 用 `ttyWCH|ttymxc` 的 **N 与 N+1 对调**。来源：驱动 debug 日志里
`OperatDevice recv cmd {"com":"comN"}` 后紧跟的 `open /dev/ttyXXX succed!`；
接线后 24 台全在线、每口温度基线各不相同（接错会读到邻口）。

> 早先只接了 com1（=ttyWCH1）时，其余 7 个口的配对确实不知道——**不要凭 comN↔ttymxcN 想当然**。

### 4.3 每口多台的前提（平台侧建设备）

模拟器侧 3 台就绪 ≠ 平台会来读。**平台侧必须建设备**，命名沿用平台自己的规则 `iadu_COM{口}_{地址}`：

```http
GET /cgi-bin/i_free.cgic?module=device_manage&op=add
    &u_id=iadu_COM3_2&mode=0&addr=2&channel=COM3&comsetting=9600,n,8,1&timeout=500
→ {"err_code":"0","err_msg":"操作成功"}
```

本次按 8 口 × 3 地址 = 24 台批量新增（原有 `iadu_COM1_1` 保留，新增 22 条全部成功），
结果 **24 台全部在线**，每口 3 台：

```
com1: 3/3 在线   com2: 3/3   com3: 3/3   com4: 3/3
com5: 3/3 在线   com6: 3/3   com7: 3/3   com8: 3/3
```

> 注意 `addr` 要与模拟器的 `unit_id` 对应（01/02/03 ↔ unit 1/2/3）；写错会一直离线（driver 找不到该地址）。

---

## 5. 踩坑清单（现象 → 根因 → 处置）

| # | 现象 | 根因 | 处置 / 预防 |
|---|------|------|-------------|
| 1 | 公钥登录 `Permission denied (publickey)` | root 家目录是 `/home/root`（不是 `/root`） | 公钥装到 `/home/root/.ssh/authorized_keys` |
| 2 | 仍拒绝，`/var/log/messages` 报 `Authentication refused: bad ownership or modes for directory /home/root` | 该目录属主是 `nginx`，sshd `StrictModes yes` | `chown root:root /home/root` |
| 3 | 想装 socat 发现装不上 | 无 socat/ser2net，且无编译环境 | 用 sshd pty（路线 A）或程序自带 pty（路线 B） |
| 4 | `ssh` 拿不到 `/dev/pts/N` | 没加 `-tt`（本地 stdout 是管道时不会分配 pty） | `ssh -tt ... "exec sleep 999999"` |
| 5 | 帧全错、CRC 不过 | pty 行规程在加工：echo 把自己的请求回灌、`onlcr` 把 `0x0A` 变 `0x0D 0x0A` | 远端先 `stty raw -echo -opost -onlcr` |
| 6 | 偶发"读到自己的请求" | 远端命令（shell/cat）在读 stdin，抢走了应答 | 用 `exec sleep` 占位，别让任何进程读 |
| 7 | 二进制流里混进一行 `/dev/pts/1` | 用 `echo $(tty)` 打到了 stdout | 写文件：`echo -n $(tty) > /tmp/sim_pts`，另开会话 `cat` |
| 8 | 换了 `/dev/ttyWCH1` 软链，采集毫无反应 | ioserver 启动时已 `open` 老节点，fd 不变 | **必须重启 ioserver** |
| 9 | 接了 `ttyWCH0` 却没有任何帧 | com1 实际用的是 `ttyWCH1`，com2 才是 `ttyWCH0` | 按 §4.2 的映射接；先抓 debug 日志确认 |
| 10 | 改了 `config.yaml` 但设备/端口没变 | `state.json` 是权威，不会被 config 覆盖 | 删 `data/state.json` 或 `--import-config` |
| 11 | 模拟器重启后设备一直离线 | pts 变了，ioserver 手里是旧 fd | 重启 ioserver（链式软链保证新 pts 能自动接上） |
| 12 | 设备上 `wget` 测端口一直失败 | busybox wget 对本地 HTTP 不可靠（连 nginx:80 都 rc=1） | 用 SSH 隧道 + 外部客户端验证；设备上改用 `nc` 发原始报文 |
| 13 | `od -An -tx1` 报错 | busybox `od` 不支持 `-A` | 用 `hexdump -C` 或 `od -x` |
| 14 | 脚本里 `timeout`/`setsid` 找不到 | 镜像里没有这两个命令 | 用 `nohup`；需要超时就用 `nc -w`/`dd` 自己控 |
| 15 | `netstat` 显示 `tcp6 :::18090`，怀疑没监听 IPv4 | Go 对通配地址建**双栈** socket（`bindv6only=0`） | 用 IPv4 地址实测；要 `0.0.0.0` 字样只能改代码用 `tcp4` |
| 16 | 本机直连 `10.10.197.203:18090` 不通 | PC 到该网段没有路由 | SSH 隧道或让跳板做端口映射 |
| 17 | 平台 API POST 返回 200 但 body 空 | 该 CGI 只吃 GET + query | 一律用 GET 拼 query |
| 18 | `addrcheck` 返回 `state:0/1` 以为在探总线 | 它只查"地址是否已被占用" | 真实在线看 `devstate` |
| 19 | 网关重启后服务全没了 | 没有注册 SysV 服务 | 见 §6，注册 init.d |
| 20 | `/var/log` 下的日志重启消失 | `/var/log` 是 tmpfs | 日志写 `/opt/iadu-sim/logs` |
| 21 | 平台只看到 1 台设备在线 | 平台侧只有 `iadu_COM1_1`；模拟器 3 台/口不等于平台会读 | 按 §4.3 批量 `device_manage&op=add`（22 条全部成功） |
| 22 | 某台的 U1 标签突然变成 `E201000000055555` | 极可能是采集进程重启/端口复用时的一次异常写入（当时日志有 `write ptmx: file already closed`） | 用 `PUT /api/devices/{id}/tags/1` 改回后稳定；批量部署后建议抽查一轮标签 |
| 23 | 接线后某口完全没有流量 | com↔tty 接错（尤其 com3..8 与 ttymxc 是**对调**关系） | 按 §4.2 的表接；用"每口温度基线不同"反查是否接串 |

---

## 6. 服务化（sysvinit，非 systemd）—— 已落地

实际脚本：**`/etc/init.d/iadu-sim`**（`start|stop|restart|status`），注册：`update-rc.d iadu-sim defaults 90`
→ `rc5.d/S90iadu-sim`、`rc0.d/K90iadu-sim`。

核心逻辑（与现场脚本一致）：

```sh
PAIRS="1:ttyWCH1 2:ttyWCH0 3:ttymxc3 4:ttymxc2 5:ttymxc5 6:ttymxc4 7:ttymxc7 8:ttymxc6"

start():
  pidof iadu-sim || ( cd /opt/iadu-sim && nohup bin/iadu-sim --dir /opt/iadu-sim >>logs/stdout.log 2>&1 & )
  等 run/com8 出现（最多 15s）
  若进程没起来或 run/com1 不存在 → 打印"启动失败，跳过接线"并 return 1（**保留真实串口**）
  逐口 wire：备份 .real → rm → ln -s run/comN
  若 ioserver 在跑 → killall + runios.sh（开机时它还没起，交给 S99rc.local）

stop():
  kill 进程 → 逐口 unwire（删软链、把 .real 移回）→ 重启 ioserver
```

```sh
chmod +x /etc/init.d/iadu-sim
update-rc.d iadu-sim defaults 90
/etc/init.d/iadu-sim status      # → running + 已接线端口: 8/8
```

**实测 stop/start**：

```
/etc/init.d/iadu-sim stop   → 已接线端口: 0/8，8 个节点恢复为字符设备，24 台离线
/etc/init.d/iadu-sim start  → 已接线端口: 8/8，24 台全部恢复在线
```

开机顺序：`S90iadu-sim` 早于 `S99rc.local`（iosdaemon 拉起 ioserver），
所以开机时服务只"起模拟器 + 接线"，ioserver 随后启动时自然打开软链 —— 不需要在脚本里抢跑 ioserver。

> 自动接线的前置条件：com↔tty 映射确认、平台侧设备已建。否则会"接上了但平台不读"。

---

## 7. 运维手册

| 场景 | 命令 |
|------|------|
| 看状态 | `pidof iadu-sim`、`netstat -lntp \| grep -E "18090\|15020"`、`ls -l /opt/iadu-sim/run/` |
| 看端口统计 | `GET /api/ports`（rx/tx/crc_errors/connected） |
| 看帧 | `GET /api/frames?limit=20&port=COM1` |
| 看设备 | `GET /api/devices`、`GET /api/devices/{id}` |
| 改值 | `PUT /api/devices/{id}/climate/override`、`PUT .../tags/{u}`、`POST .../pulse-changed` |
| 重启模拟器 | `kill $(pidof iadu-sim)` → 重新 nohup；**然后重启 ioserver** |
| 重启采集 | `cd /opt/iadu && killall -9 ioserver && ./runios.sh` |
| 干净重播种 | 停 → `rm -f data/state.json*` → 起 |
| 回滚接线 | `rm /dev/ttyWCH1 && mv /dev/ttyWCH1.real /dev/ttyWCH1` → 重启 ioserver |

**健康判据**（四条同时满足）：`devstate=1`、`existucount>0`、模拟器 `connected=true 且 crc_errors=0`、
`log_Warn_*.log` 不再出现 `Comm Failure`。

---

## 8. 安全与边界

- `0.0.0.0` 会暴露管理页与影子寄存器读写（同网段任何人可调值）；远程访问优先 `ssh -L` 隧道，
  必须对外时加 `server.web.token` 并限源 IP。Modbus TCP **无认证**。
- pty 只验证协议/业务链，**不验证波特率、电气层、时序**；A/B 反接、终端电阻、共地必须真机验证。
- 模拟器是测试工具，**不能**作为生产 Modbus 设备或安全边界。
- 本次为便于反复联调，在网关上额外留了：root 免密公钥（`/home/root/.ssh/authorized_keys`）、
  `/home/root` 属主改为 root。不需要时应清理。

---

## 9. 可复用脚本

| 脚本 | 用途 |
|------|------|
| `gen_config.py` | 生成 N 端口 × M 台的 `config.yaml`（本仓库 data/ 或本次交付物里） |
| `probe_pty.py` | 设备本地 python2.7 逐口逐台 RTU 探测（8×3=24 次，本次 24/24 通过） |
| `sim_bridge.py` | 路线 A：SSH-pty 桥（PC 侧） |
| `test_client.py` | 逐字节比对 Python 参考实现与 Go 实现 |

---

## 10. 附：本次部署的关键证据

```
--selftest                 → 17 项全 PASS（含 pty 回环、发现帧 CRC 与实测一致）
--check-config             → 8 个端口，24 个设备，24 条绑定
run/                       → com1..com8 → /dev/pts/1..8
probe_pty.py 8 3           → RESULT 24/24 ok（每口 addr=01/02/03，ESN 各不相同）
网关 device_manage query   → devstate=1, existucount=42, topTH=24/50, midTH=23/51, buttonTH=22.5/52.5
模拟器 /api/ports          → COM1 connected=True rx=310 tx=312 crc_errors=0
```

命令速查：

```sh
# 起
cd /opt/iadu-sim && nohup ./bin/iadu-sim --dir /opt/iadu-sim >> logs/stdout.log 2>&1 &
# 接线（com1）
mv /dev/ttyWCH1 /dev/ttyWCH1.real; ln -s /opt/iadu-sim/run/com1 /dev/ttyWCH1
cd /opt/iadu && killall -9 ioserver && ./runios.sh
# 回滚
rm /dev/ttyWCH1; mv /dev/ttyWCH1.real /dev/ttyWCH1
cd /opt/iadu && killall -9 ioserver && ./runios.sh
```
