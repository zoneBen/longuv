package framelog

import (
	"fmt"
	"testing"
)

func TestRingOverwriteAndSince(t *testing.T) {
	r := New(3)
	if r.Size() != 3 {
		t.Fatalf("size = %d", r.Size())
	}
	for i := 1; i <= 5; i++ {
		r.Append(Entry{Hex: fmt.Sprintf("f%d", i)})
	}
	if r.Len() != 3 {
		t.Fatalf("len = %d", r.Len())
	}
	if r.LatestSeq() != 5 {
		t.Fatalf("seq = %d", r.LatestSeq())
	}
	last := r.Last(0)
	if len(last) != 3 || last[0].Hex != "f3" || last[2].Hex != "f5" {
		t.Fatalf("last = %+v", last)
	}
	since := r.Since(3)
	if len(since) != 2 || since[0].Hex != "f4" || since[1].Hex != "f5" {
		t.Fatalf("since = %+v", since)
	}
	if got := r.Last(2); len(got) != 2 || got[0].Hex != "f4" {
		t.Fatalf("last(2) = %+v", got)
	}
	if e := r.Append(Entry{Hex: "f6"}); e.Seq != 6 || e.Time.IsZero() {
		t.Fatalf("append result = %+v", e)
	}
}

func TestRingDefaultSize(t *testing.T) {
	if got := New(0).Size(); got != 2048 {
		t.Fatalf("default size = %d", got)
	}
}
