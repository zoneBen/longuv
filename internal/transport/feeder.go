// Package transport moves bytes between physical/virtual serial ports (and
// optional TCP carriers) and the simulated register bank.
package transport

import (
	"log/slog"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/modbus"
	"github.com/zoneBen/longuv/internal/model"
)

// Feeder turns a byte stream into RTU transactions for one port.
type Feeder struct {
	Port config.Port
	Hub  *hub.Hub
	Ring *framelog.Ring
	Log  *slog.Logger
}

// NewFeeder constructs a feeder for one configured port.
func NewFeeder(port config.Port, h *hub.Hub, ring *framelog.Ring, log *slog.Logger) *Feeder {
	if log == nil {
		log = slog.Default()
	}
	return &Feeder{Port: port, Hub: h, Ring: ring, Log: log}
}

// Feed consumes every complete frame currently buffered in pending and
// returns the concatenated RTU responses.
func (f *Feeder) Feed(pending *[]byte) []byte {
	var out []byte
	for {
		need, ready := modbus.FrameLength(*pending)
		if !ready || need <= 0 || need > len(*pending) {
			break
		}
		frame := make([]byte, need)
		copy(frame, (*pending)[:need])
		if !modbus.CRCOK(frame) {
			f.record("rx", frame, false, "CRC 错误")
			f.Hub.RecordCRCError(f.Port.Name)
			*pending = (*pending)[1:]
			continue
		}
		*pending = (*pending)[need:]
		f.Hub.RecordRX(f.Port.Name, len(frame))
		f.record("rx", frame, true, "")
		for _, resp := range f.dispatch(frame) {
			out = append(out, resp...)
			f.Hub.RecordTX(f.Port.Name, len(resp))
			f.record("tx", resp, true, "")
		}
	}
	return out
}

func (f *Feeder) dispatch(frame []byte) [][]byte {
	addr := frame[0]
	var responses [][]byte

	switch addr {
	case 0xFF:
		bindings := f.Hub.BindingsForPort(f.Port.Name)
		for _, b := range bindings {
			dev, ok := f.Hub.Device(b.DeviceID)
			if !ok {
				continue
			}
			resp := f.handle(b, dev, frame)
			if resp == nil {
				continue
			}
			if f.Port.BroadcastMode == "first" && len(responses) > 0 {
				continue
			}
			responses = append(responses, resp)
		}
	case 0x00:
		// Standard broadcast: execute writes on every bound device silently.
		for _, b := range f.Hub.BindingsForPort(f.Port.Name) {
			if dev, ok := f.Hub.Device(b.DeviceID); ok {
				_ = f.handle(b, dev, frame)
			}
		}
	default:
		b, ok := f.Hub.BindingFor(f.Port.Name, addr)
		if !ok {
			return nil
		}
		dev, ok := f.Hub.Device(b.DeviceID)
		if !ok {
			return nil
		}
		if resp := f.handle(b, dev, frame); resp != nil {
			responses = append(responses, resp)
		}
	}
	return responses
}

func (f *Feeder) handle(b model.Binding, dev *model.Device, frame []byte) []byte {
	slave := &modbus.Slave{
		Addr:          b.UnitID,
		Bank:          dev,
		BcastReplyOwn: f.Port.BcastReply == "own",
		AllowAdopt:    dev.AdoptAddress(),
		OnAdopt: func(newUnit uint8) error {
			if err := f.Hub.Rebind(f.Port.Name, b.UnitID, newUnit); err != nil {
				f.Log.Warn("地址协商失败",
					"port", f.Port.Name, "device", dev.ID(),
					"old", b.UnitID, "new", newUnit, "err", err)
				return err
			}
			f.Log.Info("地址协商成功",
				"port", f.Port.Name, "device", dev.ID(),
				"old", b.UnitID, "new", newUnit)
			return nil
		},
	}
	return slave.Handle(frame)
}

func (f *Feeder) record(dir string, b []byte, ok bool, errMsg string) {
	if f.Ring == nil {
		return
	}
	e := framelog.Entry{
		Transport: "rtu",
		Port:      f.Port.Name,
		Dir:       dir,
		Hex:       hexForLog(b),
		OK:        ok,
		Error:     errMsg,
	}
	if len(b) >= 2 {
		e.Unit = b[0]
		e.FC = b[1]
	}
	f.Ring.Append(e)
}

func hexForLog(b []byte) string {
	const max = 256
	if len(b) > max {
		return modbus.Hex(b[:max]) + " …"
	}
	return modbus.Hex(b)
}
