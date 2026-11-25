package app

import (
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/zoneBen/longuv/internal/climate"
	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/modbus"
	"github.com/zoneBen/longuv/internal/model"
	"github.com/zoneBen/longuv/internal/pty"
)

// RunSelftest performs an offline protocol/environment check used by
// `iadu-sim --selftest`. It never touches the configured ports or state file.
func RunSelftest() error {
	cfg := config.Default(os.TempDir())
	seed := cfg.Devices[0]
	disabled := false
	seed.Climate.Enabled = &disabled
	seed.Fixed = config.FixedTH{Temp: 26.5, Humi: 48.0}
	seed.ESN = "iADU-SIM-00001"
	snap, err := hub.SnapshotFromConfig(seed)
	if err != nil {
		return selftestFail("配置转换", err)
	}
	dev, err := model.New(snap, time.Now(), nil)
	if err != nil {
		return selftestFail("构建设备", err)
	}
	dev.SetPrimaryAddress(1)
	slave := &modbus.Slave{Addr: 1, Bank: dev, BcastReplyOwn: true, AllowAdopt: false}

	discover, _ := hex.DecodeString("FF040150000A")
	discover = modbus.AppendCRC(discover)
	if got := fmt.Sprintf("%X", discover); got != "FF040150000A643E" {
		return selftestFail("发现帧 CRC", fmt.Errorf("得到 %s", got))
	}
	resp := slave.Handle(discover)
	if err := checkResponse(resp, 25); err != nil {
		return selftestFail("广播发现应答", err)
	}
	fmt.Println("PASS 发现帧 CRC 与实测一致 (FF040150000A643E)")
	fmt.Println("PASS 广播发现应答（地址/FC/20 字节 ESN）")

	reads := []struct {
		name string
		pdu  string
	}{
		{"单播 ESN（0x0150×10）", "01040150000A"},
		{"U1-U16 标签（0x0000×64）", "010400000040"},
		{"U17-U32 标签（0x0040×64）", "010400400040"},
		{"U33-U42 标签（0x0080×40）", "010400800028"},
		{"在位状态（FC02 0x0000×42）", "01020000002A"},
		{"变化状态（FC02 0x0100×42）", "01020100002A"},
		{"LED 回读（FC03 0x0100×42）", "01030100002A"},
		{"温湿度（FC04 0x0100×3）", "010401000003"},
		{"总线状态（FC04 0x0103×3）", "010401030003"},
		{"设备信息（FC03 0x0000×6）", "010300000006"},
	}
	for _, tc := range reads {
		req, err := hex.DecodeString(tc.pdu)
		if err != nil {
			return selftestFail(tc.name, err)
		}
		r := slave.Handle(modbus.AppendCRC(req))
		if err := checkResponse(r, 5); err != nil {
			return selftestFail(tc.name, err)
		}
		fmt.Printf("PASS %s\n", tc.name)
	}

	pduTH, _ := hex.DecodeString("010401000003")
	thResp := slave.Handle(modbus.AppendCRC(pduTH))
	if len(thResp) < 9 || thResp[3] != 0x85 || thResp[4] != 0x60 {
		return selftestFail("温湿度编码", fmt.Errorf("应答 %s", modbus.Hex(thResp)))
	}
	if climate.TempFromRaw(thResp[3]) != 26.5 || climate.HumiFromRaw(thResp[4]) != 48.0 {
		return selftestFail("温湿度解码", fmt.Errorf("得到 %.1f/%.1f",
			climate.TempFromRaw(thResp[3]), climate.HumiFromRaw(thResp[4])))
	}
	fmt.Println("PASS 温湿度编码 26.5℃ / 48.0%RH (0x8560)")

	pduInfo, _ := hex.DecodeString("010300000006")
	infoResp := slave.Handle(modbus.AppendCRC(pduInfo))
	if len(infoResp) < 15 || infoResp[3] != 0x80 || infoResp[4] != 0x20 ||
		infoResp[9] != 0x00 || infoResp[10] != 42 {
		return selftestFail("设备信息", fmt.Errorf("应答 %s", modbus.Hex(infoResp)))
	}
	fmt.Println("PASS 设备信息 型号=0x8020 U位长度=42")

	pduWrite, _ := hex.DecodeString("010601000010")
	if r := slave.Handle(modbus.AppendCRC(pduWrite)); r == nil {
		return selftestFail("LED 写入", fmt.Errorf("无应答"))
	}
	pduRead, _ := hex.DecodeString("010301000001")
	ledRead := slave.Handle(modbus.AppendCRC(pduRead))
	if len(ledRead) < 7 || ledRead[3] != 0x00 || ledRead[4] != 0x10 {
		return selftestFail("LED 回读", fmt.Errorf("应答 %s", modbus.Hex(ledRead)))
	}
	fmt.Println("PASS LED 写入/回读 (0x0010)")

	if err := testPTY(); err != nil {
		return selftestFail("pty 回环", err)
	}
	if pty.IsSupported() {
		fmt.Println("PASS pty 创建/读写回环")
	} else {
		fmt.Println("SKIP pty 回环（当前平台不支持）")
	}

	fmt.Println("SELFTEST OK")
	return nil
}

func checkResponse(resp []byte, minLen int) error {
	if resp == nil {
		return fmt.Errorf("无应答")
	}
	if len(resp) < minLen {
		return fmt.Errorf("应答过短: %s", modbus.Hex(resp))
	}
	if !modbus.CRCOK(resp) {
		return fmt.Errorf("应答 CRC 错误: %s", modbus.Hex(resp))
	}
	return nil
}

func selftestFail(name string, err error) error {
	fmt.Printf("FAIL %s: %v\n", name, err)
	return fmt.Errorf("%s: %w", name, err)
}
