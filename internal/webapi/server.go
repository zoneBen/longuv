// Package webapi serves the embedded management console, its REST API and the
// live SSE stream.
package webapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/bits"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zoneBen/longuv/internal/climate"
	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/model"
	"github.com/zoneBen/longuv/internal/transport"
	"github.com/zoneBen/longuv/internal/webui"
)

// Deps are the runtime services the web layer exposes.
type Deps struct {
	Hub        *hub.Hub
	Config     *config.Config
	ConfigPath string
	Ring       *framelog.Ring
	MBAP       *transport.MBAPServer
	Log        *slog.Logger
	Version    string
	Commit     string
	Started    time.Time
	SaveNow    func() error
}

// Server is the HTTP management server.
type Server struct {
	deps    Deps
	mux     http.Handler
	sse     *broadcaster
	limiter *ipLimiter

	mu       sync.Mutex
	listener net.Listener
	httpSrv  *http.Server

	stop chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

// New constructs a web server. Call Start to bind the listener.
func New(deps Deps) *Server {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.Started.IsZero() {
		deps.Started = time.Now()
	}
	maxSSE := 64
	if deps.Config != nil {
		maxSSE = deps.Config.Server.Web.SSEMaxClients
	}
	s := &Server{
		deps:    deps,
		sse:     newBroadcaster(maxSSE),
		limiter: newIPLimiter(120),
		stop:    make(chan struct{}),
	}
	s.mux = s.buildMux()
	return s
}

// Start binds the configured address and begins serving.
func (s *Server) Start() error {
	listen := s.deps.Config.Server.Web.Listen
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("Web 监听 %s: %w", listen, err)
	}
	s.mu.Lock()
	s.listener = ln
	s.httpSrv = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	srv := s.httpSrv
	s.mu.Unlock()

	s.wg.Add(1)
	go s.runEvents()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.deps.Log.Error("Web 服务异常退出", "err", err)
		}
	}()
	s.deps.Log.Info("管理页面已就绪", "listen", ln.Addr().String())
	return nil
}

// Addr returns the bound address, or the configured one before Start.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	if s.deps.Config != nil {
		return s.deps.Config.Server.Web.Listen
	}
	return ""
}

// Stop shuts down the HTTP server and waits for handlers.
func (s *Server) Stop() {
	s.once.Do(func() {
		close(s.stop)
		s.mu.Lock()
		srv := s.httpSrv
		s.mu.Unlock()
		if srv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Shutdown(ctx)
			cancel()
		}
		s.wg.Wait()
		s.deps.Log.Info("管理页面已停止")
	})
}

// Handler returns the middleware-wrapped mux, useful for httptest.
func (s *Server) Handler() http.Handler {
	return s.middleware(s.mux)
}

func (s *Server) buildMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("GET /api/devices", s.handleDevices)
	mux.HandleFunc("POST /api/devices", s.handleCreateDevice)
	mux.HandleFunc("GET /api/devices/{id}", s.handleDevice)
	mux.HandleFunc("PATCH /api/devices/{id}", s.handlePatchDevice)
	mux.HandleFunc("DELETE /api/devices/{id}", s.handleDeleteDevice)
	mux.HandleFunc("PUT /api/devices/{id}/climate", s.handleClimate)
	mux.HandleFunc("PUT /api/devices/{id}/climate/override", s.handleClimateOverride)
	mux.HandleFunc("DELETE /api/devices/{id}/climate/override", s.handleClearOverride)
	mux.HandleFunc("PUT /api/devices/{id}/tags/{u}", s.handleTag)
	mux.HandleFunc("PUT /api/devices/{id}/occupancy", s.handleOccupancy)
	mux.HandleFunc("PUT /api/devices/{id}/led/{u}", s.handleLED)
	mux.HandleFunc("PUT /api/devices/{id}/bus", s.handleBus)
	mux.HandleFunc("POST /api/devices/{id}/pulse-changed", s.handlePulse)
	mux.HandleFunc("POST /api/devices/{id}/changed/clear", s.handleClearChanged)
	mux.HandleFunc("POST /api/bindings", s.handleAddBinding)
	mux.HandleFunc("DELETE /api/bindings", s.handleRemoveBinding)
	mux.HandleFunc("GET /api/ports", s.handlePorts)
	mux.HandleFunc("GET /api/frames", s.handleFrames)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "接口不存在")
	})
	mux.Handle("/", http.FileServer(http.FS(webui.Static())))
	return mux
}

func (s *Server) middleware(next http.Handler) http.Handler {
	const csp = "default-src 'self'; script-src 'self'; style-src 'self'; " +
		"img-src 'self' data:; font-src 'self'; connect-src 'self'; " +
		"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", csp)
		h.Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if !s.limiter.allow(clientIP(r)) {
				writeError(w, http.StatusTooManyRequests, "rate_limited", "请求过于频繁，请稍后再试")
				return
			}
			if !s.authorized(r) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "需要有效的访问令牌")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	token := s.deps.Config.Server.Web.Token
	if token == "" {
		return true
	}
	got := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	}
	if got == "" {
		got = r.Header.Get("X-IADU-Token")
	}
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// ---------------------------------------------------------------- SSE

type broadcaster struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
	max     int
}

func newBroadcaster(max int) *broadcaster {
	if max <= 0 {
		max = 64
	}
	return &broadcaster{clients: make(map[chan []byte]struct{}), max: max}
}

func (b *broadcaster) add() (chan []byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.clients) >= b.max {
		return nil, false
	}
	ch := make(chan []byte, 64)
	b.clients[ch] = struct{}{}
	return ch, true
}

func (b *broadcaster) remove(ch chan []byte) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
}

func (b *broadcaster) send(event string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	msg := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, data))
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (s *Server) runEvents() {
	defer s.wg.Done()
	if s.deps.Ring == nil {
		return
	}
	snapshotTicker := time.NewTicker(time.Second)
	frameTicker := time.NewTicker(250 * time.Millisecond)
	defer snapshotTicker.Stop()
	defer frameTicker.Stop()
	lastSeq := s.deps.Ring.LatestSeq()
	for {
		select {
		case <-s.stop:
			return
		case <-snapshotTicker.C:
			s.sse.send("snapshot", s.buildSummary())
		case <-frameTicker.C:
			entries := s.deps.Ring.Since(lastSeq)
			if len(entries) == 0 {
				continue
			}
			if len(entries) > 100 {
				entries = entries[len(entries)-100:]
			}
			lastSeq = entries[len(entries)-1].Seq
			s.sse.send("frames", map[string]any{"frames": entries})
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unsupported", "当前服务不支持流式响应")
		return
	}
	ch, ok := s.sse.add()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "too_many_streams", "实时连接数已达上限")
		return
	}
	defer s.sse.remove(ch)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	_ = writeSSE(w, "hello", map[string]any{
		"version": s.deps.Version,
		"commit":  s.deps.Commit,
		"ts":      time.Now(),
	})
	_ = writeSSE(w, "snapshot", s.buildSummary())
	flusher.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stop:
			return
		case msg := <-ch:
			if _, err := w.Write(msg); err != nil {
				return
			}
			flusher.Flush()
		case <-ping.C:
			if _, err := io.WriteString(w, "event: ping\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, event string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	return err
}

// ---------------------------------------------------------------- summary

func (s *Server) buildSummary() Summary {
	out := Summary{
		Version:    s.deps.Version,
		Commit:     s.deps.Commit,
		UptimeS:    int64(time.Since(s.deps.Started).Seconds()),
		WebListen:  s.Addr(),
		ConfigPath: s.deps.ConfigPath,
		StateFile:  s.deps.Config.Server.StateFile,
		Ports:      s.deps.Hub.PortInfos(),
		Devices:    make([]DeviceSummary, 0),
	}
	if s.deps.MBAP != nil {
		v := s.deps.MBAP.Stats()
		out.MBAP = &v
		out.TCPListen = v.Listen
	}
	byDevice := make(map[string][]model.Binding)
	for _, b := range s.deps.Hub.Bindings() {
		byDevice[b.DeviceID] = append(byDevice[b.DeviceID], b)
	}
	for _, d := range s.deps.Hub.Devices() {
		st := d.Status()
		ds := DeviceSummary{
			ID:             d.ID(),
			Name:           d.Name(),
			Bindings:       byDevice[d.ID()],
			TCPUnitID:      d.TCPUnitID(),
			UCount:         d.UCount(),
			PresentMask:    st.Present,
			PresentCount:   bits.OnesCount64(st.Present),
			ChangedMask:    st.Changed,
			TH:             zonesFromRaw(st.THRaw),
			ManualMask:     st.ManualMask,
			ClimateEnabled: d.ClimateEnabled(),
			Reads:          st.Reads,
			Writes:         st.Writes,
		}
		if !st.LastSeen.IsZero() {
			t := st.LastSeen
			ds.LastSeen = &t
		}
		out.Devices = append(out.Devices, ds)
	}
	return out
}

func zonesFromRaw(raw [climate.Zones]uint16) ZoneTH {
	mk := func(w uint16) THView {
		tr := uint8(w >> 8)
		hr := uint8(w)
		return THView{
			Temp:    climate.TempFromRaw(tr),
			Humi:    climate.HumiFromRaw(hr),
			TempRaw: tr,
			HumiRaw: hr,
		}
	}
	return ZoneTH{Top: mk(raw[0]), Mid: mk(raw[1]), Bottom: mk(raw[2])}
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeData(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, envelope{OK: true, Data: data})
}

func writeCreated(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusCreated, envelope{OK: true, Data: data})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, envelope{OK: false, Error: &apiError{Code: code, Message: message}})
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON: "+err.Error())
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体包含多余内容")
		return false
	}
	return true
}
