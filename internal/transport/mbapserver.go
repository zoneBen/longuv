package transport

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/modbus"
	"github.com/zoneBen/longuv/internal/model"
)

// MBAPStats holds the adjustment server counters.
type MBAPStats struct {
	RX          atomic.Uint64
	TX          atomic.Uint64
	RXBytes     atomic.Uint64
	TXBytes     atomic.Uint64
	Errors      atomic.Uint64
	Connections atomic.Int64
}

// MBAPStatsView is a serialisable snapshot of MBAPStats.
type MBAPStatsView struct {
	Listen      string `json:"listen"`
	RX          uint64 `json:"rx"`
	TX          uint64 `json:"tx"`
	RXBytes     uint64 `json:"rx_bytes"`
	TXBytes     uint64 `json:"tx_bytes"`
	Errors      uint64 `json:"errors"`
	ActiveConns int64  `json:"active_conns"`
}

// MBAPServer is the Modbus TCP shadow-register adjustment service.
type MBAPServer struct {
	cfg  config.ModbusTCP
	hub  *hub.Hub
	ring *framelog.Ring
	log  *slog.Logger

	mu       sync.Mutex
	listener net.Listener
	conns    map[net.Conn]struct{}
	sem      chan struct{}
	stop     chan struct{}
	wg       sync.WaitGroup
	once     sync.Once
	stats    MBAPStats
}

// NewMBAPServer constructs an unstarted TCP adjustment server.
func NewMBAPServer(cfg config.ModbusTCP, h *hub.Hub, ring *framelog.Ring, log *slog.Logger) *MBAPServer {
	if log == nil {
		log = slog.Default()
	}
	maxConns := cfg.MaxConns
	if maxConns <= 0 {
		maxConns = 64
	}
	return &MBAPServer{
		cfg:   cfg,
		hub:   h,
		ring:  ring,
		log:   log,
		conns: make(map[net.Conn]struct{}),
		sem:   make(chan struct{}, maxConns),
		stop:  make(chan struct{}),
	}
}

// Start begins listening.
func (s *MBAPServer) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("Modbus TCP 监听 %s: %w", s.cfg.Listen, err)
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()
	s.log.Info("Modbus TCP 调整服务已就绪", "listen", ln.Addr().String(), "max_conns", cap(s.sem))
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// Addr returns the bound listener address (useful when the port is 0).
func (s *MBAPServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Stop closes the listener and every active connection.
func (s *MBAPServer) Stop() {
	s.once.Do(func() {
		close(s.stop)
		s.mu.Lock()
		if s.listener != nil {
			_ = s.listener.Close()
		}
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
		s.log.Info("Modbus TCP 调整服务已停止")
	})
}

// Stats returns a point-in-time snapshot.
func (s *MBAPServer) Stats() MBAPStatsView {
	return MBAPStatsView{
		Listen:      s.Addr(),
		RX:          s.stats.RX.Load(),
		TX:          s.stats.TX.Load(),
		RXBytes:     s.stats.RXBytes.Load(),
		TXBytes:     s.stats.TXBytes.Load(),
		Errors:      s.stats.Errors.Load(),
		ActiveConns: s.stats.Connections.Load(),
	}
}

func (s *MBAPServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		select {
		case <-s.stop:
			s.mu.Unlock()
			_ = conn.Close()
			return
		default:
		}
		select {
		case s.sem <- struct{}{}:
		default:
			s.mu.Unlock()
			_ = conn.Close()
			s.log.Warn("Modbus TCP 连接数已满，拒绝新连接", "remote", conn.RemoteAddr())
			continue
		}
		s.conns[conn] = struct{}{}
		s.stats.Connections.Add(1)
		s.wg.Add(1)
		s.mu.Unlock()
		go s.handleConn(conn)
	}
}

func (s *MBAPServer) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		_ = conn.Close()
		<-s.sem
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		s.stats.Connections.Add(-1)
	}()

	idle := time.Duration(s.cfg.IdleTimeoutS) * time.Second
	if idle <= 0 {
		idle = 120 * time.Second
	}
	for {
		_ = conn.SetReadDeadline(time.Now().Add(idle))
		m, err := modbus.ReadMBAP(conn)
		if err != nil {
			if !errors.Is(err, io.EOF) && !isTimeout(err) {
				s.stats.Errors.Add(1)
				s.log.Debug("Modbus TCP 读取结束", "remote", conn.RemoteAddr(), "err", err)
			}
			return
		}
		s.stats.RX.Add(1)
		s.stats.RXBytes.Add(uint64(modbus.MBAPHeaderLen + len(m.PDU)))
		s.record("rx", m.UnitID, m.PDU, true, "")

		resp := s.handlePDU(m.UnitID, m.PDU)
		s.stats.TX.Add(1)
		s.stats.TXBytes.Add(uint64(modbus.MBAPHeaderLen + len(resp)))
		s.record("tx", m.UnitID, resp, true, "")
		if err := modbus.WriteMBAP(conn, &modbus.MBAP{
			TxID: m.TxID, UnitID: m.UnitID, PDU: resp,
		}); err != nil {
			return
		}
	}
}

func (s *MBAPServer) handlePDU(unit uint8, pdu []byte) []byte {
	if len(pdu) == 0 {
		return mbapException(0, modbus.ExcIllegalValue)
	}
	fc := pdu[0]
	dev, ok := s.resolveDevice(unit)
	if !ok {
		s.log.Debug("Modbus TCP 未知 unit", "unit", unit)
		return mbapException(fc, modbus.ExcGatewayTarget)
	}

	switch fc {
	case 0x01, 0x02:
		if len(pdu) != 5 {
			return mbapException(fc, modbus.ExcIllegalValue)
		}
		start := binary.BigEndian.Uint16(pdu[1:3])
		count := binary.BigEndian.Uint16(pdu[3:5])
		if count < 1 || count > 2000 || int(start)+int(count) > 0x10000 {
			return mbapException(fc, modbus.ExcIllegalValue)
		}
		var bits []bool
		var code uint8
		if fc == 0x01 {
			bits, code = dev.ReadShadowCoils(start, count)
		} else {
			bits, code = dev.ReadShadowDiscrete(start, count)
		}
		if code != 0 {
			return mbapException(fc, code)
		}
		return bitsPDU(fc, bits)

	case 0x03, 0x04:
		if len(pdu) != 5 {
			return mbapException(fc, modbus.ExcIllegalValue)
		}
		start := binary.BigEndian.Uint16(pdu[1:3])
		count := binary.BigEndian.Uint16(pdu[3:5])
		if count < 1 || count > 125 || int(start)+int(count) > 0x10000 {
			return mbapException(fc, modbus.ExcIllegalValue)
		}
		var words []uint16
		var code uint8
		if fc == 0x03 {
			words, code = dev.ReadShadowHolding(start, count)
		} else {
			words, code = dev.ReadShadowInput(start, count)
		}
		if code != 0 {
			return mbapException(fc, code)
		}
		return wordsPDU(fc, words)

	case 0x05, 0x06:
		if len(pdu) != 5 {
			return mbapException(fc, modbus.ExcIllegalValue)
		}
		addr := binary.BigEndian.Uint16(pdu[1:3])
		val := binary.BigEndian.Uint16(pdu[3:5])
		var code uint8
		if fc == 0x05 {
			if val != 0xFF00 && val != 0x0000 {
				return mbapException(fc, modbus.ExcIllegalValue)
			}
			code = dev.WriteShadowCoil(addr, val == 0xFF00)
		} else {
			code = dev.WriteShadowHolding(addr, val)
		}
		if code != 0 {
			return mbapException(fc, code)
		}
		return append([]byte(nil), pdu...)

	case 0x0F:
		if len(pdu) < 6 {
			return mbapException(fc, modbus.ExcIllegalValue)
		}
		start := binary.BigEndian.Uint16(pdu[1:3])
		count := binary.BigEndian.Uint16(pdu[3:5])
		byteCount := int(pdu[5])
		if count < 1 || count > 1968 || int(start)+int(count) > 0x10000 ||
			byteCount != int(count+7)/8 || len(pdu) != 6+byteCount {
			return mbapException(fc, modbus.ExcIllegalValue)
		}
		for i := 0; i < int(count); i++ {
			on := pdu[6+i/8]&(1<<uint(i%8)) != 0
			if code := dev.WriteShadowCoil(start+uint16(i), on); code != 0 {
				return mbapException(fc, code)
			}
		}
		return mbapMultiPDU(fc, start, count)

	case 0x10:
		if len(pdu) < 6 {
			return mbapException(fc, modbus.ExcIllegalValue)
		}
		start := binary.BigEndian.Uint16(pdu[1:3])
		count := binary.BigEndian.Uint16(pdu[3:5])
		byteCount := int(pdu[5])
		if count < 1 || count > 123 || int(start)+int(count) > 0x10000 ||
			byteCount != int(count)*2 || len(pdu) != 6+byteCount {
			return mbapException(fc, modbus.ExcIllegalValue)
		}
		for i := 0; i < int(count); i++ {
			v := binary.BigEndian.Uint16(pdu[6+2*i:])
			if code := dev.WriteShadowHolding(start+uint16(i), v); code != 0 {
				return mbapException(fc, code)
			}
		}
		return mbapMultiPDU(fc, start, count)

	default:
		return mbapException(fc, modbus.ExcIllegalFunction)
	}
}

func (s *MBAPServer) resolveDevice(unit uint8) (*model.Device, bool) {
	u := unit
	if u == 0 || u == 0xFF {
		u = s.cfg.DefaultUnit
	}
	return s.hub.DeviceByTCPUnit(u)
}

func (s *MBAPServer) record(dir string, unit uint8, pdu []byte, ok bool, errMsg string) {
	if s.ring == nil {
		return
	}
	e := framelog.Entry{
		Transport: "mbap",
		Port:      "tcp",
		Dir:       dir,
		Unit:      unit,
		Hex:       hexForLog(pdu),
		OK:        ok,
		Error:     errMsg,
	}
	if len(pdu) > 0 {
		e.FC = pdu[0]
	}
	s.ring.Append(e)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func mbapException(fc, code uint8) []byte {
	return []byte{fc | 0x80, code}
}

func bitsPDU(fc uint8, bits []bool) []byte {
	bc := (len(bits) + 7) / 8
	out := make([]byte, 2+bc)
	out[0] = fc
	out[1] = byte(bc)
	for i, v := range bits {
		if v {
			out[2+i/8] |= 1 << uint(i%8)
		}
	}
	return out
}

func wordsPDU(fc uint8, words []uint16) []byte {
	out := make([]byte, 2+2*len(words))
	out[0] = fc
	out[1] = byte(2 * len(words))
	for i, v := range words {
		binary.BigEndian.PutUint16(out[2+2*i:], v)
	}
	return out
}

func mbapMultiPDU(fc uint8, start, count uint16) []byte {
	out := make([]byte, 5)
	out[0] = fc
	binary.BigEndian.PutUint16(out[1:3], start)
	binary.BigEndian.PutUint16(out[3:5], count)
	return out
}
