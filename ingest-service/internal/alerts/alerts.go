// Package alerts turns a stream of readings into alerts such as
// "vehicle is overheating" or "battery is draining too fast".
//
// The engine keeps a short in-memory history per vehicle. It is used by a
// single writer goroutine, so it does not need locks.
package alerts

import (
	"fmt"
	"time"

	"github.com/sathishreddypidugu/fleet-telemetry/ingest-service/internal/telemetry"
)

// Alert types stored in the alerts table.
const (
	HighTemp   = "HIGH_TEMP"
	RapidDrain = "RAPID_DRAIN"
	LowBattery = "LOW_BATTERY"
)

// Alert is one problem detected for one vehicle.
type Alert struct {
	VehicleID string
	Timestamp time.Time
	Type      string
	Value     float64
	Message   string
}

// Rules holds the thresholds. Defaults() gives sensible values.
type Rules struct {
	MaxTempC      float64       // alert when temperature goes above this
	LowBatteryPct float64       // alert when battery goes below this
	DrainPct      float64       // alert when battery drops by more than this...
	DrainWindow   time.Duration // ...within this time window
	Cooldown      time.Duration // do not repeat the same alert for the same vehicle within this time
}

func Defaults() Rules {
	return Rules{
		MaxTempC:      60,
		LowBatteryPct: 10,
		DrainPct:      10,
		DrainWindow:   5 * time.Minute,
		Cooldown:      5 * time.Minute,
	}
}

type sample struct {
	ts      time.Time
	battery float64
}

type vehicleState struct {
	history   []sample             // battery samples inside DrainWindow, oldest first
	lastFired map[string]time.Time // alert type -> last time it fired
}

// Engine evaluates rules against readings.
type Engine struct {
	rules    Rules
	vehicles map[string]*vehicleState
}

func NewEngine(r Rules) *Engine {
	return &Engine{rules: r, vehicles: map[string]*vehicleState{}}
}

// Evaluate checks one reading and returns any alerts it triggers.
// Readings for one vehicle should arrive roughly in time order.
func (e *Engine) Evaluate(r telemetry.Reading) []Alert {
	st := e.vehicles[r.VehicleID]
	if st == nil {
		st = &vehicleState{lastFired: map[string]time.Time{}}
		e.vehicles[r.VehicleID] = st
	}

	var out []Alert
	fire := func(kind string, value float64, msg string) {
		if last, ok := st.lastFired[kind]; ok && r.Timestamp.Sub(last) < e.rules.Cooldown {
			return // still in cooldown, avoid alert spam
		}
		st.lastFired[kind] = r.Timestamp
		out = append(out, Alert{VehicleID: r.VehicleID, Timestamp: r.Timestamp, Type: kind, Value: value, Message: msg})
	}

	// Rule 1: overheating.
	if r.TempC > e.rules.MaxTempC {
		fire(HighTemp, r.TempC, fmt.Sprintf("Temperature %.1f°C is above %.0f°C", r.TempC, e.rules.MaxTempC))
	}

	// Rule 2: low battery.
	if r.BatteryPct < e.rules.LowBatteryPct {
		fire(LowBattery, r.BatteryPct, fmt.Sprintf("Battery at %.1f%%", r.BatteryPct))
	}

	// Rule 3: rapid drain. Keep a sliding window of battery samples and
	// compare the highest value in the window with the current value.
	st.history = append(st.history, sample{ts: r.Timestamp, battery: r.BatteryPct})
	cutoff := r.Timestamp.Add(-e.rules.DrainWindow)
	i := 0
	for i < len(st.history) && st.history[i].ts.Before(cutoff) {
		i++
	}
	st.history = st.history[i:]

	maxInWindow := r.BatteryPct
	for _, s := range st.history {
		if s.battery > maxInWindow {
			maxInWindow = s.battery
		}
	}
	if drop := maxInWindow - r.BatteryPct; drop > e.rules.DrainPct {
		fire(RapidDrain, drop, fmt.Sprintf("Battery dropped %.1f%% in under %s", drop, e.rules.DrainWindow))
	}

	return out
}

// TrackedVehicles is exposed for metrics and tests.
func (e *Engine) TrackedVehicles() int { return len(e.vehicles) }
