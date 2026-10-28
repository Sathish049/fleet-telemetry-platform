package telemetry

import (
	"testing"
	"time"
)

func validReading(now time.Time) Reading {
	return Reading{
		VehicleID: "VH-0001", Timestamp: now,
		SpeedKph: 65, BatteryPct: 80, TempC: 35,
		Lat: 37.49, Lng: -121.94,
	}
}

func TestValidate(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		mutate  func(*Reading)
		wantErr bool
	}{
		{"valid reading", func(r *Reading) {}, false},
		{"missing vehicle id", func(r *Reading) { r.VehicleID = "" }, true},
		{"missing timestamp", func(r *Reading) { r.Timestamp = time.Time{} }, true},
		{"timestamp in future", func(r *Reading) { r.Timestamp = now.Add(10 * time.Minute) }, true},
		{"small clock skew ok", func(r *Reading) { r.Timestamp = now.Add(30 * time.Second) }, false},
		{"timestamp too old", func(r *Reading) { r.Timestamp = now.Add(-48 * time.Hour) }, true},
		{"negative speed", func(r *Reading) { r.SpeedKph = -1 }, true},
		{"battery over 100", func(r *Reading) { r.BatteryPct = 101 }, true},
		{"battery at 0 ok", func(r *Reading) { r.BatteryPct = 0 }, false},
		{"temp too high", func(r *Reading) { r.TempC = 200 }, true},
		{"bad latitude", func(r *Reading) { r.Lat = 91 }, true},
		{"bad longitude", func(r *Reading) { r.Lng = -181 }, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := validReading(now)
			tc.mutate(&r)
			err := r.Validate(now)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
