# iadu-sim

iADU 42U 资产条 Modbus 模拟器（Go）。同一份寄存器状态同时通过：

- **Modbus RTU 从站**（纯 Go pty 虚拟串口，多串口、多从站 ID）
- **Modbus TCP**（MBAP 影子寄存器区，用于调值/验证）

对外暴露，并内置中文管理页面（go:embed + SSE）。

## 状态

开发中（里程碑 M0）。完整设计与交付说明在实现完成后补全。

## 快速构建

```bash
go build ./cmd/iadu-sim
./iadu-sim --dump-config          # 查看默认配置
./iadu-sim --dir ./.iadu-sim      # 生成并加载默认配置
```

## 许可

内部测试工具，未附带开源许可。
