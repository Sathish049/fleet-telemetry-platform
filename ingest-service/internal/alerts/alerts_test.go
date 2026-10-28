package alerts

import (
	"testing"
	"time"

	"github.com/sathishreddypidugu/fleet-telemetry/ingest-service/internal/telemetry"
)

var t0 = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func reading(offset time.Duration, battery, temp float64) telemetry.Reading {
	return telemetry.Reading{VehicleID: "VH-0001", Timestamp: t0.Add(offset), BatteryPct: battery, TempC: temp}
}

func types(as []Alert) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Type)
	}
	return out
}

func TestNoAlertsForNormalDriving(t *testing.T) {
	e := NewEngine(Defaults())
	for i := 0; i < 60; i++ {
		if got := e.Evaluate(reading(time.Duration(i)*time.Second, 80-float64(i)*0.01, 35)); len(got) != 0 {
			t.Fatalf("unexpected alerts at step %d: %v", i, types(got))
		}
	}
}

func TestHighTempFiresOnceThenCoolsDown(t *testing.T) {
	e := NewEngine(Defaults())
	if got := e.Evaluate(reading(0, 80, 65)); len(got) != 1 || got[0].Type != HighTemp {
		t.Fatalf("want one HIGH_TEMP, got %v", types(got))
	}
	// Still hot one minute later: inside cooldown, no repeat.
	if got := e.Evaluate(reading(time.Minute, 80, 66)); len(got) != 0 {
		t.Fatalf("want no alert during cooldown, got %v", types(got))
	}
	// Six minutes later: cooldown over, fire again.
	if got := e.Evaluate(reading(6*time.Minute, 80, 67)); len(got) != 1 {
		t.Fatalf("want alert after cooldown, got %v", types(got))
	}
}

func TestLowBattery(t *testing.T) {
	e := NewEngine(Defaults())
	got := e.Evaluate(reading(0, 8, 30))
	if len(got) != 1 || got[0].Type != LowBattery {
		t.Fatalf("want LOW_BATTERY, got %v", types(got))
	}
}

func TestRapidDrainInsideWindow(t *testing.T) {
	e := NewEngine(Defaults())
	e.Evaluate(reading(0, 70, 30))
	e.Evaluate(reading(2*time.Minute, 65, 30))
	got := e.Evaluate(reading(4*time.Minute, 58, 30)) // 12% drop in 4 minutes
	if len(got) != 1 || got[0].Type != RapidDrain {
		t.Fatalf("want RAPID_DRAIN, got %v", types(got))
	}
	if got[0].Value < 11.9 || got[0].Value > 12.1 {
		t.Fatalf("want drop of 12, got %.2f", got[0].Value)
	}
}

func TestSlowDrainOutsideWindowIsIgnored(t *testing.T) {
	e := NewEngine(Defaults())
	e.Evaluate(reading(0, 70, 30))
	got := e.Evaluate(reading(10*time.Minute, 58, 30)) // 12% but over 10 minutes
	if len(got) != 0 {
		t.Fatalf("want no alert, got %v", types(got))
	}
}

func TestVehiclesAreIndependent(t *testing.T) {
	e := NewEngine(Defaults())
	a := reading(0, 80, 65)
	b := reading(0, 80, 65)
	b.VehicleID = "VH-0002"
	if len(e.Evaluate(a)) != 1 || len(e.Evaluate(b)) != 1 {
		t.Fatal("each vehicle should get its own alert")
	}
	if e.TrackedVehicles() != 2 {
		t.Fatalf("want 2 tracked vehicles, got %d", e.TrackedVehicles())
	}
}
