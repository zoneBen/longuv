package webapi

import (
	"time"

	"github.com/zoneBen/longuv/internal/climate"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/model"
	"github.com/zoneBen/longuv/internal/transport"
)

type envelope struct {
	OK    bool      `json:"ok"`
	Data  any       `json:"data,omitempty"`
	Error *apiError `json:"error,omitempty"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// THView is one decoded temperature/humidity zone.
type THView struct {
	Temp    float64 `json:"temp"`
	Humi    float64 `json:"humi"`
	TempRaw uint8   `json:"temp_raw"`
	HumiRaw uint8   `json:"humi_raw"`
}

// ZoneTH groups the three sensor zones.
type ZoneTH struct {
	Top    THView `json:"top"`
	Mid    THView `json:"mid"`
	Bottom THView `json:"bottom"`
}

// DeviceSummary is the overview representation of a device.
type DeviceSummary struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Bindings       []model.Binding `json:"bindings"`
	TCPUnitID      uint8           `json:"tcp_unit_id"`
	UCount         int             `json:"u_count"`
	PresentMask    uint64          `json:"present"`
	PresentCount   int             `json:"present_count"`
	ChangedMask    uint64          `json:"changed"`
	TH             ZoneTH          `json:"th"`
	ManualMask     uint8           `json:"manual_mask"`
	ClimateEnabled bool            `json:"climate_enabled"`
	LastSeen       *time.Time      `json:"last_seen,omitempty"`
	Reads          uint64          `json:"reads"`
	Writes         uint64          `json:"writes"`
}

// Summary is the full live snapshot pushed over SSE and returned by
// /api/summary.
type Summary struct {
	Version    string                   `json:"version"`
	Commit     string                   `json:"commit"`
	UptimeS    int64                    `json:"uptime_s"`
	WebListen  string                   `json:"web_listen"`
	TCPListen  string                   `json:"tcp_listen,omitempty"`
	ConfigPath string                   `json:"config_path"`
	StateFile  string                   `json:"state_file"`
	MBAP       *transport.MBAPStatsView `json:"mbap,omitempty"`
	Ports      []hub.PortInfo           `json:"ports"`
	Devices    []DeviceSummary          `json:"devices"`
}

// DeviceDetail is the full editable device representation.
type DeviceDetail struct {
	Snapshot model.Snapshot   `json:"snapshot"`
	Status   model.StatusView `json:"status"`
	Bindings []model.Binding  `json:"bindings"`
	TH       ZoneTH           `json:"th"`
}

type createDeviceRequest struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	UCount    int           `json:"u_count"`
	ESN       string        `json:"esn"`
	TCPUnitID uint8         `json:"tcp_unit_id"`
	Occupancy string        `json:"occupancy"`
	Bindings  []bindingSpec `json:"bindings"`
}

type bindingSpec struct {
	Port   string `json:"port"`
	UnitID uint8  `json:"unit_id"`
}

type bindingRequest struct {
	Device string `json:"device"`
	Port   string `json:"port"`
	UnitID uint8  `json:"unit_id"`
}

type devicePatch struct {
	Name          *string `json:"name"`
	ESN           *string `json:"esn"`
	Model         *uint16 `json:"model"`
	BaudCode      *uint16 `json:"baud_code"`
	SoftVersion   *uint16 `json:"soft_version"`
	HardVersion   *uint16 `json:"hard_version"`
	UCount        *int    `json:"u_count"`
	TCPUnitID     *uint8  `json:"tcp_unit_id"`
	AdoptAddress  *bool   `json:"adopt_address"`
	Strict        *bool   `json:"strict"`
	ChangedHoldMS *int    `json:"changed_hold_ms"`
}

type tagRequest struct {
	Tag     string `json:"tag"`
	Present *bool  `json:"present"`
}

type occupancyRequest struct {
	Bits    string `json:"bits"`
	U       int    `json:"u"`
	Present *bool  `json:"present"`
}

type ledRequest struct {
	Value uint16 `json:"value"`
}

type busRequest struct {
	Connected   *bool `json:"connected"`
	CommOK      *bool `json:"comm_ok"`
	Overcurrent *bool `json:"overcurrent"`
}

type pulseRequest struct {
	U int `json:"u"`
}

type overrideRequest struct {
	Zone      string   `json:"zone"`
	Temp      *float64 `json:"temp"`
	Humi      *float64 `json:"humi"`
	DurationS int      `json:"duration_s"`
}

type climateRequest struct {
	climate.Params
	Fixed *[2]float64 `json:"fixed,omitempty"`
}
