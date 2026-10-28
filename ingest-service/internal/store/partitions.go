package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The telemetry table is partitioned by day: one partition per date, named
// pYYYYMMDD, plus a catch-all "pmax" partition.
//
// Why partition?
//   - Queries on a time range only touch the partitions for those days
//     (MySQL "partition pruning"), so they stay fast as data grows.
//   - Deleting old data is instant: DROP PARTITION removes a whole day
//     without a slow DELETE that scans and locks millions of rows.

const partitionLayout = "20060102"

// PartitionPlan says which daily partitions to add and which to drop.
type PartitionPlan struct {
	Add  []time.Time // days to create, ascending
	Drop []string    // partition names to drop
}

// PlanPartitions is a pure function (no database), so it is easy to test.
// existing holds current partition names (pmax is ignored).
func PlanPartitions(existing []string, today time.Time, daysAhead, retentionDays int) PartitionPlan {
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)

	var have []time.Time
	for _, name := range existing {
		if !strings.HasPrefix(name, "p") || name == "pmax" {
			continue
		}
		if d, err := time.Parse(partitionLayout, name[1:]); err == nil {
			have = append(have, d)
		}
	}
	sort.Slice(have, func(i, j int) bool { return have[i].Before(have[j]) })

	var plan PartitionPlan

	// New partitions can only be split off the end (from pmax), so only add
	// days after the newest partition we already have.
	var newest time.Time
	if len(have) > 0 {
		newest = have[len(have)-1]
	}
	for d := today; !d.After(today.AddDate(0, 0, daysAhead)); d = d.AddDate(0, 0, 1) {
		if d.After(newest) {
			plan.Add = append(plan.Add, d)
		}
	}

	oldest := today.AddDate(0, 0, -retentionDays)
	for _, d := range have {
		if d.Before(oldest) {
			plan.Drop = append(plan.Drop, "p"+d.Format(partitionLayout))
		}
	}
	return plan
}

// MaintainPartitions creates upcoming daily partitions and drops expired ones.
func (s *Store) MaintainPartitions(ctx context.Context, now time.Time, daysAhead, retentionDays int) (PartitionPlan, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT PARTITION_NAME FROM information_schema.PARTITIONS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'telemetry' AND PARTITION_NAME IS NOT NULL`)
	if err != nil {
		return PartitionPlan{}, err
	}
	var existing []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return PartitionPlan{}, err
		}
		existing = append(existing, n)
	}
	rows.Close()

	plan := PlanPartitions(existing, now.UTC(), daysAhead, retentionDays)

	if len(plan.Add) > 0 {
		parts := make([]string, 0, len(plan.Add)+1)
		for _, d := range plan.Add {
			next := d.AddDate(0, 0, 1).Format("2006-01-02")
			parts = append(parts, fmt.Sprintf("PARTITION p%s VALUES LESS THAN (TO_DAYS('%s'))", d.Format(partitionLayout), next))
		}
		parts = append(parts, "PARTITION pmax VALUES LESS THAN MAXVALUE")
		q := "ALTER TABLE telemetry REORGANIZE PARTITION pmax INTO (" + strings.Join(parts, ", ") + ")"
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return plan, fmt.Errorf("add partitions: %w", err)
		}
	}
	if len(plan.Drop) > 0 {
		q := "ALTER TABLE telemetry DROP PARTITION " + strings.Join(plan.Drop, ", ")
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return plan, fmt.Errorf("drop partitions: %w", err)
		}
	}
	return plan, nil
}
