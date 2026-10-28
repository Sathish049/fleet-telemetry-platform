package store

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	d, _ := time.Parse("2006-01-02", s)
	return d
}

func TestPlanPartitionsFreshTable(t *testing.T) {
	plan := PlanPartitions([]string{"pmax"}, day("2026-09-27"), 3, 7)
	if len(plan.Add) != 4 { // today + 3 days ahead
		t.Fatalf("want 4 partitions, got %d", len(plan.Add))
	}
	if !plan.Add[0].Equal(day("2026-09-27")) || !plan.Add[3].Equal(day("2026-09-30")) {
		t.Fatalf("wrong range: %v .. %v", plan.Add[0], plan.Add[3])
	}
	if len(plan.Drop) != 0 {
		t.Fatalf("nothing to drop on a fresh table, got %v", plan.Drop)
	}
}

func TestPlanPartitionsOnlyAddsNewDaysAndDropsOld(t *testing.T) {
	existing := []string{"p20260915", "p20260920", "p20260927", "p20260928", "pmax"}
	plan := PlanPartitions(existing, day("2026-09-27"), 3, 7)

	if len(plan.Add) != 2 || !plan.Add[0].Equal(day("2026-09-29")) {
		t.Fatalf("want to add 29th and 30th, got %v", plan.Add)
	}
	// Retention is 7 days -> keep 2026-09-20 onwards, drop the 15th.
	if len(plan.Drop) != 1 || plan.Drop[0] != "p20260915" {
		t.Fatalf("want to drop p20260915, got %v", plan.Drop)
	}
}

func TestPlanPartitionsNoopWhenUpToDate(t *testing.T) {
	existing := []string{"p20260927", "p20260928", "p20260929", "p20260930", "pmax"}
	plan := PlanPartitions(existing, day("2026-09-27"), 3, 7)
	if len(plan.Add) != 0 || len(plan.Drop) != 0 {
		t.Fatalf("want no changes, got %+v", plan)
	}
}
