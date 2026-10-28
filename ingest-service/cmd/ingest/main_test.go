package main

import "testing"

func TestDecodeSingleObject(t *testing.T) {
	rs, err := decode([]byte(`{"vehicle_id":"VH-0001","ts":"2026-09-27T12:00:00Z","battery_pct":80}`))
	if err != nil || len(rs) != 1 || rs[0].VehicleID != "VH-0001" {
		t.Fatalf("got %v, %v", rs, err)
	}
}

func TestDecodeArray(t *testing.T) {
	rs, err := decode([]byte(`  [{"vehicle_id":"A","ts":"2026-09-27T12:00:00Z"},{"vehicle_id":"B","ts":"2026-09-27T12:00:00Z"}]`))
	if err != nil || len(rs) != 2 {
		t.Fatalf("got %v, %v", rs, err)
	}
}

func TestDecodeInvalidJSON(t *testing.T) {
	if _, err := decode([]byte(`{not json`)); err == nil {
		t.Fatal("expected an error")
	}
}
