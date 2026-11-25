package webapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zoneBen/longuv/internal/climate"
	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/model"
)

func (s *Server) persist(w http.ResponseWriter) bool {
	if s.deps.SaveNow == nil {
		return true
	}
	if err := s.deps.SaveNow(); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed",
			"变更已生效，但状态保存失败: "+err.Error())
		return false
	}
	return true
}

func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) (*model.Device, bool) {
	dev, ok := s.deps.Hub.Device(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "设备不存在")
		return nil, false
	}
	return dev, true
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	var req createDeviceRequest
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "设备 id 不能为空")
		return
	}
	if _, exists := s.deps.Hub.Device(req.ID); exists {
		writeError(w, http.StatusConflict, "conflict", "设备 id 已存在")
		return
	}
	seed := config.DefaultDeviceSeed(len(s.deps.Hub.Devices())+1, s.deps.Config.Server.Timezone)
	seed.ID = req.ID
	if req.Name != "" {
		seed.Name = req.Name
	} else {
		seed.Name = req.ID
	}
	if req.UCount > 0 {
		seed.UCount = req.UCount
	}
	if req.ESN != "" {
		seed.ESN = req.ESN
	}
	if req.Occupancy != "" {
		seed.Occupancy = req.Occupancy
	}
	if req.TCPUnitID != 0 {
		seed.TCPUnitID = req.TCPUnitID
	} else {
		seed.TCPUnitID = nextTCPUnit(s.deps.Hub)
	}
	if seed.TCPUnitID == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "没有可用的 tcp_unit_id")
		return
	}
	snap, err := hub.SnapshotFromConfig(seed)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.deps.Hub.AddDevice(snap); err != nil {
		writeError(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	for _, b := range req.Bindings {
		if err := s.deps.Hub.AddBinding(model.Binding{
			DeviceID: req.ID, Port: b.Port, UnitID: b.UnitID,
		}); err != nil {
			_ = s.deps.Hub.RemoveDevice(req.ID)
			writeError(w, http.StatusBadRequest, "binding_failed", err.Error())
			return
		}
	}
	dev, ok := s.deps.Hub.Device(req.ID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "设备创建后无法读取")
		return
	}
	if !s.persist(w) {
		return
	}
	writeCreated(w, s.deviceDetail(dev))
}

func (s *Server) handlePatchDevice(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.getDevice(w, r)
	if !ok {
		return
	}
	var req devicePatch
	if !readJSON(w, r, &req) {
		return
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "设备名称不能为空")
			return
		}
		dev.SetName(*req.Name)
	}
	if req.ESN != nil {
		if err := dev.SetESN(*req.ESN); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	if req.UCount != nil {
		if err := dev.SetUCount(*req.UCount); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	if req.TCPUnitID != nil {
		if err := s.deps.Hub.SetTCPUnit(dev.ID(), *req.TCPUnitID); err != nil {
			writeError(w, http.StatusConflict, "conflict", err.Error())
			return
		}
	}
	if req.Model != nil || req.BaudCode != nil || req.SoftVersion != nil || req.HardVersion != nil {
		snap := dev.Snapshot()
		m, b, sv, hv := snap.Model, snap.BaudCode, snap.SoftVersion, snap.HardVersion
		if req.Model != nil {
			m = *req.Model
		}
		if req.BaudCode != nil {
			b = *req.BaudCode
		}
		if req.SoftVersion != nil {
			sv = *req.SoftVersion
		}
		if req.HardVersion != nil {
			hv = *req.HardVersion
		}
		dev.SetInfoValues(m, b, sv, hv)
	}
	if req.AdoptAddress != nil {
		dev.SetAdoptAddress(*req.AdoptAddress)
	}
	if req.Strict != nil {
		dev.SetStrict(*req.Strict)
	}
	if req.ChangedHoldMS != nil {
		if err := dev.SetChangedHoldMS(*req.ChangedHoldMS); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	if !s.persist(w) {
		return
	}
	writeData(w, s.deviceDetail(dev))
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.deps.Hub.RemoveDevice(id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if !s.persist(w) {
		return
	}
	writeData(w, map[string]any{"deleted": id})
}

func (s *Server) handleTag(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.getDevice(w, r)
	if !ok {
		return
	}
	u, ok := parseU(w, r, dev)
	if !ok {
		return
	}
	var req tagRequest
	if !readJSON(w, r, &req) {
		return
	}
	tag, err := model.ParseTagHex(req.Tag)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := dev.SetTag(u, tag); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Present != nil {
		if err := dev.SetPresent(u, *req.Present); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	if !s.persist(w) {
		return
	}
	writeData(w, s.deviceDetail(dev))
}

func (s *Server) handleOccupancy(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.getDevice(w, r)
	if !ok {
		return
	}
	var req occupancyRequest
	if !readJSON(w, r, &req) {
		return
	}
	switch {
	case req.Bits != "":
		mask, err := config.ParseOccupancy(req.Bits, dev.UCount())
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		for u := 1; u <= dev.UCount(); u++ {
			_ = dev.SetPresent(u, mask&(uint64(1)<<uint(u-1)) != 0)
		}
	case req.U > 0 && req.Present != nil:
		if err := dev.SetPresent(req.U, *req.Present); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "需要 bits 或 u+present")
		return
	}
	if !s.persist(w) {
		return
	}
	writeData(w, s.deviceDetail(dev))
}

func (s *Server) handleLED(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.getDevice(w, r)
	if !ok {
		return
	}
	u, ok := parseU(w, r, dev)
	if !ok {
		return
	}
	var req ledRequest
	if !readJSON(w, r, &req) {
		return
	}
	if err := dev.SetLED(u, req.Value); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !s.persist(w) {
		return
	}
	writeData(w, s.deviceDetail(dev))
}

func (s *Server) handleBus(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.getDevice(w, r)
	if !ok {
		return
	}
	var req busRequest
	if !readJSON(w, r, &req) {
		return
	}
	snap := dev.Snapshot()
	connected := snap.Bus[0] != 0
	commOK := snap.Bus[1] != 0
	overcurrent := snap.Bus[2] != 0
	if req.Connected != nil {
		connected = *req.Connected
	}
	if req.CommOK != nil {
		commOK = *req.CommOK
	}
	if req.Overcurrent != nil {
		overcurrent = *req.Overcurrent
	}
	dev.SetBus(connected, commOK, overcurrent)
	if !s.persist(w) {
		return
	}
	writeData(w, s.deviceDetail(dev))
}

func (s *Server) handleClimate(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.getDevice(w, r)
	if !ok {
		return
	}
	var req climateRequest
	if !readJSON(w, r, &req) {
		return
	}
	if err := climate.ValidateParams(req.Params); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Fixed != nil {
		if err := dev.SetFixedTH(req.Fixed[0], req.Fixed[1]); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	dev.SetClimateParams(req.Params)
	if !s.persist(w) {
		return
	}
	writeData(w, s.deviceDetail(dev))
}

func (s *Server) handleClimateOverride(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.getDevice(w, r)
	if !ok {
		return
	}
	var req overrideRequest
	if !readJSON(w, r, &req) {
		return
	}
	zones, ok := parseZones(req.Zone)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "zone 必须是 top/mid/bottom/all")
		return
	}
	st := dev.Status()
	for _, z := range zones {
		word := st.THRaw[z]
		temp := climate.TempFromRaw(uint8(word >> 8))
		humi := climate.HumiFromRaw(uint8(word))
		if req.Temp != nil {
			temp = *req.Temp
		}
		if req.Humi != nil {
			humi = *req.Humi
		}
		if temp < -40 || temp > 87.5 || humi < 0 || humi > 100 {
			writeError(w, http.StatusBadRequest, "bad_request", "温湿度超出范围")
			return
		}
		var until time.Time
		if req.DurationS > 0 {
			until = time.Now().Add(time.Duration(req.DurationS) * time.Second)
		}
		dev.SetManual(z, climate.QuantTemp(temp), climate.QuantHumi(humi), until)
	}
	if !s.persist(w) {
		return
	}
	writeData(w, s.deviceDetail(dev))
}

func (s *Server) handleClearOverride(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.getDevice(w, r)
	if !ok {
		return
	}
	zones, ok := parseZones(r.URL.Query().Get("zone"))
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "zone 必须是 top/mid/bottom/all")
		return
	}
	for _, z := range zones {
		dev.ClearManual(z)
	}
	if !s.persist(w) {
		return
	}
	writeData(w, s.deviceDetail(dev))
}

func (s *Server) handlePulse(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.getDevice(w, r)
	if !ok {
		return
	}
	var req pulseRequest
	if r.ContentLength > 0 {
		if !readJSON(w, r, &req) {
			return
		}
	}
	if req.U > 0 {
		if err := dev.PulseChanged(req.U); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	} else {
		dev.PulseAllChanged()
	}
	writeData(w, s.deviceDetail(dev))
}

func (s *Server) handleClearChanged(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.getDevice(w, r)
	if !ok {
		return
	}
	dev.ClearChanged()
	writeData(w, s.deviceDetail(dev))
}

func (s *Server) handleAddBinding(w http.ResponseWriter, r *http.Request) {
	var req bindingRequest
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.deps.Hub.AddBinding(model.Binding{
		DeviceID: req.Device, Port: req.Port, UnitID: req.UnitID,
	}); err != nil {
		writeError(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	if !s.persist(w) {
		return
	}
	dev, ok := s.deps.Hub.Device(req.Device)
	if !ok {
		writeData(w, map[string]any{"added": true})
		return
	}
	writeData(w, s.deviceDetail(dev))
}

func (s *Server) handleRemoveBinding(w http.ResponseWriter, r *http.Request) {
	var req bindingRequest
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.deps.Hub.RemoveBinding(req.Device, req.Port, req.UnitID); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if !s.persist(w) {
		return
	}
	dev, ok := s.deps.Hub.Device(req.Device)
	if !ok {
		writeData(w, map[string]any{"removed": true})
		return
	}
	writeData(w, s.deviceDetail(dev))
}

func parseU(w http.ResponseWriter, r *http.Request, dev *model.Device) (int, bool) {
	u, err := strconv.Atoi(r.PathValue("u"))
	if err != nil || u < 1 || u > dev.UCount() {
		writeError(w, http.StatusBadRequest, "bad_request", "U 位编号超出范围")
		return 0, false
	}
	return u, true
}

func parseZones(s string) ([]climate.Zone, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "all":
		return []climate.Zone{climate.Top, climate.Mid, climate.Bottom}, true
	case "top":
		return []climate.Zone{climate.Top}, true
	case "mid":
		return []climate.Zone{climate.Mid}, true
	case "bottom":
		return []climate.Zone{climate.Bottom}, true
	default:
		return nil, false
	}
}

func nextTCPUnit(h *hub.Hub) uint8 {
	used := make(map[uint8]bool)
	for _, d := range h.Devices() {
		used[d.TCPUnitID()] = true
	}
	for u := 1; u <= 247; u++ {
		if !used[uint8(u)] {
			return uint8(u)
		}
	}
	return 0
}
