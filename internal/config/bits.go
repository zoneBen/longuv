package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ParseOccupancy parses "all", "none" or comma-separated 1-based ranges
// (e.g. "1-10,20") into a little-endian bit mask.
func ParseOccupancy(s string, uCount int) (uint64, error) {
	if uCount < 1 || uCount > 64 {
		return 0, fmt.Errorf("U 位长度 %d 必须在 1..64 之间", uCount)
	}
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "all":
		return (uint64(1) << uint(uCount)) - 1, nil
	case "none":
		return 0, nil
	}

	var mask uint64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		low, high := part, part
		if i := strings.IndexByte(part, '-'); i >= 0 {
			low = strings.TrimSpace(part[:i])
			high = strings.TrimSpace(part[i+1:])
		}
		lo, err := strconv.Atoi(low)
		if err != nil {
			return 0, fmt.Errorf("占用区间 %q 不是合法数字", part)
		}
		hi, err := strconv.Atoi(high)
		if err != nil {
			return 0, fmt.Errorf("占用区间 %q 不是合法数字", part)
		}
		if lo < 1 || hi > uCount || lo > hi {
			return 0, fmt.Errorf("占用区间 %q 超出范围 1-%d", part, uCount)
		}
		for u := lo; u <= hi; u++ {
			mask |= uint64(1) << uint(u-1)
		}
	}
	return mask, nil
}

// FormatOccupancy renders a bit mask in the "1-10,20" form understood by
// ParseOccupancy.
func FormatOccupancy(mask uint64, uCount int) string {
	if uCount <= 0 {
		return "none"
	}
	all := (uint64(1) << uint(uCount)) - 1
	effective := mask & all
	switch effective {
	case 0:
		return "none"
	case all:
		return "all"
	}

	var parts []string
	start := -1
	for i := 0; i <= uCount; i++ {
		set := i < uCount && effective&(uint64(1)<<uint(i)) != 0
		if set && start < 0 {
			start = i
			continue
		}
		if !set && start >= 0 {
			lo, hi := start+1, i
			if lo == hi {
				parts = append(parts, strconv.Itoa(lo))
			} else {
				parts = append(parts, fmt.Sprintf("%d-%d", lo, hi))
			}
			start = -1
		}
	}
	return strings.Join(parts, ",")
}

// ParseTagHex parses an 8-byte tag from 16 hexadecimal characters.
func ParseTagHex(s string) ([8]byte, error) {
	var out [8]byte
	s = strings.TrimSpace(s)
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

// ParseSeed parses the 8-byte tag seed used by auto tag generation.
func ParseSeed(s string) ([8]byte, error) { return ParseTagHex(s) }

// NormalizeESN pads or truncates an ESN to exactly 20 ASCII bytes.
func NormalizeESN(s string) [20]byte {
	var out [20]byte
	for i := range out {
		out[i] = ' '
	}
	copy(out[:], []byte(s))
	return out
}

// ParseFileMode parses an octal permission string such as "0666".
func ParseFileMode(s string) (os.FileMode, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0o")
	s = strings.TrimPrefix(s, "0O")
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil || v > 0o777 {
		return 0, fmt.Errorf("文件权限 %q 不是合法的八进制权限（如 0666）", s)
	}
	return os.FileMode(v), nil
}
