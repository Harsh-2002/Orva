package pool

import "testing"

// TestMaxPidsOr guards the M2 fix: the warm-pool spawn must never pass a
// non-positive pids cap to nsjail (which would render `--cgroup_pids_max 0`,
// i.e. no fork-bomb protection). maxPidsOr substitutes the fallback whenever
// the configured value is <= 0.
func TestMaxPidsOr(t *testing.T) {
	cases := []struct {
		v, fallback, want int
	}{
		{0, 32, 32},   // unset config → fallback
		{-1, 32, 32},  // defensive: negative → fallback
		{64, 32, 64},  // explicit positive value wins
		{1, 32, 1},    // any positive value is honored
	}
	for _, c := range cases {
		if got := maxPidsOr(c.v, c.fallback); got != c.want {
			t.Errorf("maxPidsOr(%d, %d) = %d, want %d", c.v, c.fallback, got, c.want)
		}
	}
}
