// Package telemetry defines the message a vehicle sends and the rules for
// deciding whether a message is valid.
package telemetry

import (
	"errors"
	"fmt"
	"time"
)

// Reading is one telemetry sample from one vehicle.
type Reading struct {
	VehicleID  string    `json:"vehicle_id"`
	Timestamp  time.Time `json:"ts"`
	SpeedKph   float64   `json:"speed_kph"`
	BatteryPct float64   `json:"battery_pct"`
	TempC      float64   `json:"temp_c"`
	Lat        float64   `json:"lat"`
	Lng        float64   `json:"lng"`
}

// Allowed clock skew between the vehicle and the server.
const (
	maxFutureSkew = 2 * time.Minute
	maxAge        = 24 * time.Hour
)

var ErrMissingVehicleID = errors.New("vehicle_id is required")

// Validate returns an error describing the first problem with the reading,
// or nil if the reading can be accepted. now is passed in so tests are
// deterministic.
func (r Reading) Validate(now time.Time) error {
	switch {
	case r.VehicleID == "":
		return ErrMissingVehicleID
	case len(r.VehicleID) > 32:
		return fmt.Errorf("vehicle_id too long (%d chars)", len(r.VehicleID))
	case r.Timestamp.IsZero():
		return errors.New("ts is required")
	case r.Timestamp.After(now.Add(maxFutureSkew)):
		return fmt.Errorf("ts %s is in the future", r.Timestamp.Format(time.RFC3339))
	case r.Timestamp.Before(now.Add(-maxAge)):
		return fmt.Errorf("ts %s is older than %s", r.Timestamp.Format(time.RFC3339), maxAge)
	case r.SpeedKph < 0 || r.SpeedKph > 300:
		return fmt.Errorf("speed_kph %.1f out of range [0,300]", r.SpeedKph)
	case r.BatteryPct < 0 || r.BatteryPct > 100:
		return fmt.Errorf("battery_pct %.1f out of range [0,100]", r.BatteryPct)
	case r.TempC < -40 || r.TempC > 150:
		return fmt.Errorf("temp_c %.1f out of range [-40,150]", r.TempC)
	case r.Lat < -90 || r.Lat > 90:
		return fmt.Errorf("lat %.5f out of range", r.Lat)
	case r.Lng < -180 || r.Lng > 180:
		return fmt.Errorf("lng %.5f out of range", r.Lng)
	}
	return nil
}
