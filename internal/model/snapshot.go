// Package model implements the iADU 42U register bank and its persistence
// snapshot. It is transport-agnostic and shared by the RTU and Modbus TCP
// servers.
package model

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/zoneBen/longuv/internal/climate"
)

// MaxUCount is the largest supported asset strip height.
const MaxUCount = 42

// Snapshot is the complete persisted state of one simulated device.
type Snapshot struct {
	ID            string                             `json:"id"`
	Name          string                             `json:"name"`
	UCount        int                                `json:"u_count"`
	Model         uint16                             `json:"model"`
	BaudCode      uint16                             `json:"baud_code"`
	SoftVersion   uint16                             `json:"soft_version"`
	HardVersion   uint16                             `json:"hard_version"`
	ESN           string                             `json:"esn"`
	AdoptAddress  bool                               `json:"adopt_address"`
	ChangedHoldMS int                                `json:"changed_hold_ms"`
	TCPUnitID     uint8                              `json:"tcp_unit_id"`
	Strict        bool                               `json:"strict"`
	Tags          []string                           `json:"tags"`
	Present       uint64                             `json:"present"`
	Bus           [3]uint16                          `json:"bus"`
	LED           []uint16                           `json:"led"`
	Climate       climate.Params                     `json:"climate"`
	Manual        [climate.Zones]climate.ManualState `json:"manual"`
	FixedTH       [2]float64                         `json:"fixed_th"`
}

// Binding attaches a device to a port at a Modbus unit id.
type Binding struct {
	DeviceID string `json:"device"`
	Port     string `json:"port"`
	UnitID   uint8  `json:"unit_id"`
}

// GenerateTags builds the 8-byte tag of every U position. Present positions
// get seed+custom/index, absent positions stay zero.
func GenerateTags(uCount int, present uint64, seed [8]byte, indexOffset, indexBytes int, custom map[int][8]byte) [][8]byte {
	out := make([][8]byte, uCount)
	if indexBytes < 0 {
		indexBytes = 0
	}
	if indexBytes > 8 {
		indexBytes = 8
	}
	for i := 0; i < uCount; i++ {
		if present&(uint64(1)<<uint(i)) == 0 {
			continue
		}
		if tag, ok := custom[i+1]; ok {
			out[i] = tag
			continue
		}
		tag := seed
		u := i + 1
		for b := 0; b < indexBytes; b++ {
			off := indexOffset + indexBytes - 1 - b
			if off >= 0 && off < len(tag) {
				tag[off] = byte(u >> (8 * b))
			}
		}
		out[i] = tag
	}
	return out
}

// DefaultTag returns the deterministic tag used when a U position becomes
// present without an explicit tag.
func DefaultTag(u int) [8]byte {
	return [8]byte{0xE2, 0, 0, 0, 0, 0, byte(u >> 8), byte(u)}
}

// ParseTagHex parses an 8-byte tag; an empty string means all zeroes.
func ParseTagHex(s string) ([8]byte, error) {
	var out [8]byte
	s = strings.TrimSpace(s)
	if s == "" {
		return out, nil
	}
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	s = strings.ReplaceAll(s, " ", "")
	if len(s) != 16 {
		return out, fmt.Errorf("标签必须是 16 位十六进制（8 字节），得到 %q", s)
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return out, fmt.Errorf("标签不是合法十六进制 %q: %w", s, err)
	}
	copy(out[:], b)
	return out, nil
}

// FormatTagHex renders an 8-byte tag as uppercase hexadecimal.
func FormatTagHex(tag [8]byte) string {
	return strings.ToUpper(hex.EncodeToString(tag[:]))
}

// NormalizeESN pads or truncates an ESN to exactly 20 ASCII bytes.
func NormalizeESN(s string) [20]byte {
	var out [20]byte
	for i := range out {
		out[i] = ' '
	}
	copy(out[:], []byte(s))
	return out
}

// MaskBits returns a mask with the lowest n bits set.
func MaskBits(n int) uint64 {
	if n <= 0 {
		return 0
	}
	if n >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << uint(n)) - 1
}
