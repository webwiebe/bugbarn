package storage

import "testing"

func TestSampleRateForClimbsOneRungPerDecade(t *testing.T) {
	t.Parallel()

	const keepAll = 1000
	cases := []struct {
		name string
		n    int64
		want int64
	}{
		{"first event of an issue", 1, 1},
		{"just under the threshold", 999, 1},
		{"exactly at the threshold", 1000, 1},
		{"one past the threshold", 1001, 10},
		{"end of the first rung", 10_000, 10},
		{"start of the second rung", 10_001, 100},
		{"end of the second rung", 100_000, 100},
		{"start of the third rung", 100_001, 1000},
		{"end of the third rung", 1_000_000, 1000},
		{"start of the last rung", 1_000_001, 10_000},
		{"the ladder stops climbing", 1_000_000_000, 10_000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sampleRateFor(tc.n, keepAll); got != tc.want {
				t.Errorf("sampleRateFor(%d, %d) = %d, want %d", tc.n, keepAll, got, tc.want)
			}
		})
	}
}

// A project that has opted out, or a deployment with sampling disabled, passes a
// non-positive threshold. Nothing may be dropped in that case at any volume.
func TestSampleForKeepsEverythingWhenDisabled(t *testing.T) {
	t.Parallel()

	for _, n := range []int64{1, 1000, 1_000_001, 50_000_000} {
		keep, weight := sampleFor(n, 0)
		if !keep || weight != 1 {
			t.Errorf("sampleFor(%d, 0) = (%v, %d), want (true, 1)", n, keep, weight)
		}
	}
}

func TestSampleForBelowThresholdKeepsEveryEvent(t *testing.T) {
	t.Parallel()

	for n := int64(1); n <= 1000; n++ {
		keep, weight := sampleFor(n, 1000)
		if !keep || weight != 1 {
			t.Fatalf("sampleFor(%d, 1000) = (%v, %d), want (true, 1)", n, keep, weight)
		}
	}
}

// The property that matters: summing the weights of what we stored reconstructs
// how many events actually happened. A row lands on every multiple of K carrying
// weight K, so the sum accounts for everything up to the last multiple and
// nothing after it — it trails the truth by less than one K, and never exceeds
// it. Anything outside that band means the weights are lying about volume.
func TestSampleForWeightsReconstructTrueVolume(t *testing.T) {
	t.Parallel()

	const keepAll = 100
	for _, total := range []int64{1, 99, 100, 101, 1000, 12_345, 250_000, 1_842_279} {
		var stored, summed int64
		for n := int64(1); n <= total; n++ {
			keep, weight := sampleFor(n, keepAll)
			if keep {
				stored++
				summed += weight
			}
		}
		if stored == 0 {
			t.Fatalf("total=%d stored nothing at all", total)
		}
		k := sampleRateFor(total, keepAll)
		if summed > total || summed <= total-k {
			t.Errorf("total=%d: summed weights = %d, want within (%d, %d]", total, summed, total-k, total)
		}
		if stored > total {
			t.Errorf("total=%d: stored %d rows, more than the events themselves", total, stored)
		}
	}
}

// The headline claim from issue #181: the fingerprint that reached 1.84M events
// on production has to cost a few thousand rows, not 1.84M.
func TestSampleForBoundsTheProductionBurst(t *testing.T) {
	t.Parallel()

	const observed = 1_842_279
	var stored int64
	for n := int64(1); n <= observed; n++ {
		if keep, _ := sampleFor(n, defaultSampleAfter); keep {
			stored++
		}
	}
	if stored > 5000 {
		t.Errorf("stored %d rows for %d events; the ladder is not bounding the burst", stored, observed)
	}
	// And it must not collapse to nothing — the evidence has to survive.
	if stored < defaultSampleAfter {
		t.Errorf("stored only %d rows; every event up to the threshold should be kept", stored)
	}
}
