package webapi

import (
	"net/http"
	"strconv"

	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/model"
)

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	summary := s.buildSummary()
	writeData(w, map[string]any{
		"version":  summary.Version,
		"commit":   summary.Commit,
		"uptime_s": summary.UptimeS,
		"devices":  len(summary.Devices),
		"ports":    len(summary.Ports),
		"mbap":     summary.MBAP,
	})
}

func (s *Server) handleSummary(w http.ResponseWriter, _ *http.Request) {
	writeData(w, s.buildSummary())
}

func (s *Server) handleDevices(w http.ResponseWriter, _ *http.Request) {
	writeData(w, s.buildSummary().Devices)
}

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deps.Hub.Device(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "设备不存在")
		return
	}
	writeData(w, s.deviceDetail(dev))
}

func (s *Server) handlePorts(w http.ResponseWriter, _ *http.Request) {
	out := map[string]any{"ports": s.deps.Hub.PortInfos()}
	if s.deps.MBAP != nil {
		out["mbap"] = s.deps.MBAP.Stats()
	}
	writeData(w, out)
}

func (s *Server) handleFrames(w http.ResponseWriter, r *http.Request) {
	if s.deps.Ring == nil {
		writeData(w, map[string]any{"frames": []any{}})
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	entries := s.deps.Ring.Last(limit)
	if port := r.URL.Query().Get("port"); port != "" {
		filtered := entries[:0]
		for _, e := range entries {
			if e.Port == port {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}
	writeData(w, map[string]any{"frames": entries})
}

func (s *Server) deviceDetail(dev *model.Device) DeviceDetail {
	st := dev.Status()
	return DeviceDetail{
		Snapshot: dev.Snapshot(),
		Status:   st,
		Bindings: bindingsForDevice(s.deps.Hub, dev.ID()),
		TH:       zonesFromRaw(st.THRaw),
	}
}

func bindingsForDevice(h *hub.Hub, id string) []model.Binding {
	out := make([]model.Binding, 0, 2)
	for _, b := range h.Bindings() {
		if b.DeviceID == id {
			out = append(out, b)
		}
	}
	return out
}
