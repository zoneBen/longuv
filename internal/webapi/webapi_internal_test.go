package webapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
)

func internalLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func internalDeps(t *testing.T) Deps {
	t.Helper()
	cfg := config.Default(t.TempDir())
	cfg.Server.Web.Listen = "127.0.0.1:0"
	log := internalLogger()
	h, err := hub.New(cfg, framelog.New(16), log, nil)
	if err != nil {
		t.Fatal(err)
	}
	return Deps{Hub: h, Config: cfg, Ring: h.Ring(), Log: log, Version: "test"}
}

func TestServerStartStopAndAddr(t *testing.T) {
	deps := internalDeps(t)
	s := New(deps)
	if got := s.Addr(); got != "127.0.0.1:0" {
		t.Fatalf("Addr before Start = %q", got)
	}
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	addr := s.Addr()
	if addr == "" || addr == "127.0.0.1:0" {
		t.Fatalf("Addr after Start = %q", addr)
	}
	resp, err := http.Get("http://" + addr + "/api/health")
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
	s.Stop()
	s.Stop() // idempotent
}

func TestServerStartBindFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cfg := config.Default(t.TempDir())
	cfg.Server.Web.Listen = ln.Addr().String()
	s := New(Deps{Config: cfg, Log: internalLogger()})
	if err := s.Start(); err == nil {
		s.Stop()
		t.Fatal("Start should fail on an occupied address")
	}
}

func TestServerAddrWithoutConfig(t *testing.T) {
	s := New(Deps{Log: internalLogger()})
	if got := s.Addr(); got != "" {
		t.Fatalf("Addr without config = %q", got)
	}
	s.Stop()
}

func TestBroadcasterBehaviour(t *testing.T) {
	b := newBroadcaster(0)
	if b.max != 64 {
		t.Fatalf("default max = %d", b.max)
	}
	ch, ok := b.add()
	if !ok {
		t.Fatal("first client rejected")
	}
	b.send("hello", map[string]string{"a": "b"})
	select {
	case msg := <-ch:
		if !bytes.Contains(msg, []byte("event: hello")) {
			t.Fatalf("message = %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("no broadcast message")
	}
	b.send("bad", make(chan int)) // json.Marshal must fail without blocking
	b.remove(ch)
	b.send("after", 1)

	full := newBroadcaster(1)
	c1, ok := full.add()
	if !ok {
		t.Fatal("first client of full broadcaster rejected")
	}
	if _, ok := full.add(); ok {
		t.Fatal("second client should be rejected")
	}
	for i := 0; i < 100; i++ {
		full.send("flood", i) // channel is full and must not block
	}
	full.remove(c1)
}

func TestIPLimiter(t *testing.T) {
	l := newIPLimiter(0)
	if l.limit != 120 {
		t.Fatalf("default limit = %d", l.limit)
	}
	l = newIPLimiter(2)
	if !l.allow("a") || !l.allow("a") {
		t.Fatal("first two requests should pass")
	}
	if l.allow("a") {
		t.Fatal("third request should be limited")
	}
	if !l.allow("b") {
		t.Fatal("a different IP should pass")
	}
	l.window = time.Now().Add(-2 * time.Second)
	if !l.allow("a") {
		t.Fatal("a new window should reset the counter")
	}
}

func TestClientIPAndAuthorization(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.5:1234"
	if got := clientIP(req); got != "192.0.2.5" {
		t.Fatalf("clientIP = %q", got)
	}
	req.RemoteAddr = "not-a-host-port"
	if got := clientIP(req); got != "not-a-host-port" {
		t.Fatalf("clientIP fallback = %q", got)
	}

	cfg := config.Default(t.TempDir())
	cfg.Server.Web.Token = "secret"
	s := New(Deps{Config: cfg, Log: internalLogger()})
	if s.authorized(httptest.NewRequest("GET", "/", nil)) {
		t.Fatal("missing token should fail")
	}
	for _, header := range []string{"Authorization", "X-IADU-Token"} {
		r := httptest.NewRequest("GET", "/", nil)
		if header == "Authorization" {
			r.Header.Set(header, "Bearer secret")
		} else {
			r.Header.Set(header, "secret")
		}
		if !s.authorized(r) {
			t.Fatalf("%s token rejected", header)
		}
	}
	if !s.authorized(httptest.NewRequest("GET", "/?token=secret", nil)) {
		t.Fatal("query token rejected")
	}
	if s.authorized(httptest.NewRequest("GET", "/?token=wrong", nil)) {
		t.Fatal("wrong query token accepted")
	}
	cfg.Server.Web.Token = ""
	if !s.authorized(httptest.NewRequest("GET", "/", nil)) {
		t.Fatal("empty configured token should allow all")
	}
}

func TestReadJSONValidation(t *testing.T) {
	type payload struct {
		A int `json:"a"`
	}
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"valid", `{"a":1}`, true},
		{"trailing", `{"a":1}{"a":2}`, false},
		{"unknown field", `{"b":1}`, false},
		{"malformed", `{`, false},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		var dst payload
		if got := readJSON(rec, req, &dst); got != tc.want {
			t.Fatalf("%s readJSON = %v status=%d", tc.name, got, rec.Code)
		}
		if !tc.want && rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d", tc.name, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"a":1}`))
	req.Body = http.MaxBytesReader(rec, req.Body, 4)
	var dst payload
	if readJSON(rec, req, &dst) {
		t.Fatal("oversized body should fail")
	}
}

func TestHandleFramesRingAndFilter(t *testing.T) {
	cfg := config.Default(t.TempDir())
	ring := framelog.New(8)
	ring.Append(framelog.Entry{Port: "COM1", FC: 3})
	ring.Append(framelog.Entry{Port: "COM2", FC: 4})
	s := New(Deps{Config: cfg, Ring: ring, Log: internalLogger()})
	rec := httptest.NewRecorder()
	s.handleFrames(rec, httptest.NewRequest("GET", "/api/frames?port=COM2&limit=5", nil))
	var env struct {
		Data struct {
			Frames []framelog.Entry `json:"frames"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Frames) != 1 || env.Data.Frames[0].Port != "COM2" {
		t.Fatalf("filtered frames = %+v", env.Data.Frames)
	}
	empty := New(Deps{Config: cfg, Log: internalLogger()})
	rec = httptest.NewRecorder()
	empty.handleFrames(rec, httptest.NewRequest("GET", "/api/frames", nil))
	if !strings.Contains(rec.Body.String(), `"frames"`) {
		t.Fatalf("nil ring response = %s", rec.Body.String())
	}
}

func TestParseZones(t *testing.T) {
	cases := []struct {
		in    string
		zones int
		ok    bool
	}{
		{"", 3, true},
		{"all", 3, true},
		{"ALL", 3, true},
		{" top ", 1, true},
		{"mid", 1, true},
		{"bottom", 1, true},
		{"side", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseZones(tc.in)
		if ok != tc.ok || len(got) != tc.zones {
			t.Fatalf("parseZones(%q) = %v, %v", tc.in, got, ok)
		}
	}
}

func TestHandleEventsWithoutFlusher(t *testing.T) {
	s := New(Deps{Config: config.Default(t.TempDir()), Log: internalLogger()})
	rec := httptest.NewRecorder()
	s.handleEvents(noFlushWriter{rec}, httptest.NewRequest("GET", "/api/events", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("no-flusher status = %d", rec.Code)
	}
}

func TestHandleEventsTooManyStreams(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Server.Web.SSEMaxClients = 1
	s := New(Deps{Config: cfg, Log: internalLogger()})
	ch, ok := s.sse.add()
	if !ok {
		t.Fatal("failed to occupy the stream slot")
	}
	defer s.sse.remove(ch)
	rec := httptest.NewRecorder()
	s.handleEvents(rec, httptest.NewRequest("GET", "/api/events", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("stream limit status = %d", rec.Code)
	}
}

type noFlushWriter struct {
	http.ResponseWriter
}

func TestRunEventsBroadcasts(t *testing.T) {
	deps := internalDeps(t)
	s := New(deps)
	ch, ok := s.sse.add()
	if !ok {
		t.Fatal("failed to add SSE client")
	}
	s.wg.Add(1)
	go s.runEvents()
	time.Sleep(50 * time.Millisecond)
	defer func() {
		close(s.stop)
		s.wg.Wait()
	}()
	deps.Ring.Append(framelog.Entry{Port: "COM1", FC: 3})
	deadline := time.After(2 * time.Second)
	seenFrames, seenSnapshot := false, false
	for !seenFrames || !seenSnapshot {
		select {
		case msg := <-ch:
			if bytes.Contains(msg, []byte("event: frames")) {
				seenFrames = true
			}
			if bytes.Contains(msg, []byte("event: snapshot")) {
				seenSnapshot = true
			}
		case <-deadline:
			t.Fatalf("missing broadcasts: frames=%v snapshot=%v", seenFrames, seenSnapshot)
		}
	}
}
