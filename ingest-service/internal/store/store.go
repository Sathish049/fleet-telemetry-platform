// Package store writes telemetry and alerts to MySQL.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/sathishreddypidugu/fleet-telemetry/ingest-service/internal/alerts"
	"github.com/sathishreddypidugu/fleet-telemetry/ingest-service/internal/telemetry"
)

type Store struct {
	db *sql.DB
}

// Open connects to MySQL. All times are stored in UTC.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// rowsPerStatement keeps each INSERT well under MySQL's max_allowed_packet.
const rowsPerStatement = 500

// InsertTelemetry writes readings with one multi-row INSERT per chunk.
// The primary key is (vehicle_id, ts), and INSERT IGNORE skips rows that
// already exist, so a message delivered twice is stored only once.
// It returns how many rows were actually inserted (duplicates excluded).
func (s *Store) InsertTelemetry(ctx context.Context, rs []telemetry.Reading) (int64, error) {
	var inserted int64
	for start := 0; start < len(rs); start += rowsPerStatement {
		chunk := rs[start:min(start+rowsPerStatement, len(rs))]
		var sb strings.Builder
		sb.WriteString("INSERT IGNORE INTO telemetry (vehicle_id, ts, speed_kph, battery_pct, temp_c, lat, lng) VALUES ")
		args := make([]any, 0, len(chunk)*7)
		for i, r := range chunk {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString("(?,?,?,?,?,?,?)")
			args = append(args, r.VehicleID, r.Timestamp.UTC(), r.SpeedKph, r.BatteryPct, r.TempC, r.Lat, r.Lng)
		}
		res, err := s.db.ExecContext(ctx, sb.String(), args...)
		if err != nil {
			return inserted, fmt.Errorf("insert telemetry: %w", err)
		}
		n, _ := res.RowsAffected()
		inserted += n
	}
	return inserted, nil
}

// UpsertLatest keeps one row per vehicle with its most recent reading.
// The dashboard map reads this small table instead of scanning telemetry.
// The IF() guards make sure an older, late-arriving message never
// overwrites a newer one.
func (s *Store) UpsertLatest(ctx context.Context, rs []telemetry.Reading) error {
	latest := map[string]telemetry.Reading{}
	for _, r := range rs {
		if cur, ok := latest[r.VehicleID]; !ok || r.Timestamp.After(cur.Timestamp) {
			latest[r.VehicleID] = r
		}
	}
	if len(latest) == 0 {
		return nil
	}
	ids := make([]string, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.Strings(ids) // consistent lock order avoids deadlocks between writers

	var sb strings.Builder
	sb.WriteString("INSERT INTO vehicle_latest (vehicle_id, ts, speed_kph, battery_pct, temp_c, lat, lng) VALUES ")
	args := make([]any, 0, len(ids)*7)
	for i, id := range ids {
		r := latest[id]
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString("(?,?,?,?,?,?,?)")
		args = append(args, r.VehicleID, r.Timestamp.UTC(), r.SpeedKph, r.BatteryPct, r.TempC, r.Lat, r.Lng)
	}
	// Columns are updated in order; ts must be updated last so the other
	// IF() checks still compare against the old ts.
	sb.WriteString(` ON DUPLICATE KEY UPDATE
		speed_kph   = IF(VALUES(ts) > ts, VALUES(speed_kph),   speed_kph),
		battery_pct = IF(VALUES(ts) > ts, VALUES(battery_pct), battery_pct),
		temp_c      = IF(VALUES(ts) > ts, VALUES(temp_c),      temp_c),
		lat         = IF(VALUES(ts) > ts, VALUES(lat),         lat),
		lng         = IF(VALUES(ts) > ts, VALUES(lng),         lng),
		ts          = GREATEST(ts, VALUES(ts))`)
	_, err := s.db.ExecContext(ctx, sb.String(), args...)
	if err != nil {
		return fmt.Errorf("upsert vehicle_latest: %w", err)
	}
	return nil
}

// InsertAlerts stores alerts produced by the rules engine.
func (s *Store) InsertAlerts(ctx context.Context, as []alerts.Alert) error {
	if len(as) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("INSERT INTO alerts (vehicle_id, ts, alert_type, value, message) VALUES ")
	args := make([]any, 0, len(as)*5)
	for i, a := range as {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString("(?,?,?,?,?)")
		args = append(args, a.VehicleID, a.Timestamp.UTC(), a.Type, a.Value, a.Message)
	}
	_, err := s.db.ExecContext(ctx, sb.String(), args...)
	if err != nil {
		return fmt.Errorf("insert alerts: %w", err)
	}
	return nil
}
