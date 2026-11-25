// Package framelog keeps a bounded, concurrency-safe ring of decoded Modbus
// frames for diagnostics and the live web console.
package framelog

import (
	"sync"
	"time"
)

// Entry is one observed frame or protocol event.
type Entry struct {
	Seq        uint64    `json:"seq"`
	Time       time.Time `json:"time"`
	Transport  string    `json:"transport"` // rtu | mbap
	Port       string    `json:"port,omitempty"`
	Dir        string    `json:"dir"` // rx | tx
	Unit       uint8     `json:"unit"`
	FC         uint8     `json:"fc"`
	Hex        string    `json:"hex"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	DurationUS int64     `json:"duration_us,omitempty"`
}

// Ring is a fixed-size overwrite ring buffer.
type Ring struct {
	mu    sync.RWMutex
	buf   []Entry
	start int
	count int
	seq   uint64
}

// New creates a ring; a non-positive size selects the default of 2048.
func New(size int) *Ring {
	if size <= 0 {
		size = 2048
	}
	return &Ring{buf: make([]Entry, size)}
}

// Append stores an entry, filling Seq and Time when unset. The stored entry is
// returned so callers can broadcast the assigned sequence number.
func (r *Ring) Append(e Entry) Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	e.Seq = r.seq
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if r.count < len(r.buf) {
		r.buf[(r.start+r.count)%len(r.buf)] = e
		r.count++
	} else {
		r.buf[r.start] = e
		r.start = (r.start + 1) % len(r.buf)
	}
	return e
}

// Since returns entries with Seq greater than afterSeq, oldest first.
func (r *Ring) Since(afterSeq uint64) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry, 0, r.count)
	for i := 0; i < r.count; i++ {
		e := r.buf[(r.start+i)%len(r.buf)]
		if e.Seq > afterSeq {
			out = append(out, e)
		}
	}
	return out
}

// Last returns at most n of the newest entries, oldest first.
func (r *Ring) Last(n int) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if n <= 0 || n > r.count {
		n = r.count
	}
	out := make([]Entry, 0, n)
	for i := r.count - n; i < r.count; i++ {
		out = append(out, r.buf[(r.start+i)%len(r.buf)])
	}
	return out
}

// Len returns the number of stored entries.
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}

// LatestSeq returns the most recently assigned sequence number.
func (r *Ring) LatestSeq() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.seq
}

// Size returns the ring capacity.
func (r *Ring) Size() int { return len(r.buf) }
