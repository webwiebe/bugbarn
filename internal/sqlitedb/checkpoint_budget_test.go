package sqlitedb

import "testing"

// The per-tick budget only bounds contention if it is a fraction of the tick
// interval; a budget >= the interval would mean back-to-back checkpointing.
func TestCheckpointTickBudgetIsAFractionOfTheInterval(t *testing.T) {
	if checkpointTickBudget >= DefaultCheckpointInterval {
		t.Errorf("checkpointTickBudget %v >= DefaultCheckpointInterval %v: a tick could occupy the write connection continuously",
			checkpointTickBudget, DefaultCheckpointInterval)
	}
	if checkpointTickBudget <= checkpointRetryInterval {
		t.Errorf("checkpointTickBudget %v <= checkpointRetryInterval %v: no retry would ever get a second attempt",
			checkpointTickBudget, checkpointRetryInterval)
	}
}
