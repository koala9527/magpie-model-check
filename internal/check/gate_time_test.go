package check

import (
	"testing"
	"time"
)

func TestGateTimeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, state    string
		checked, allow time.Time
	}{
		{"fresh", "passed", time.Now(), time.Time{}},
		{"expired", "passed", time.Now().Add(-16 * time.Minute), time.Time{}},
		{"lease", "review", time.Time{}, time.Now().Add(time.Minute)},
		{"lease expired", "review", time.Time{}, time.Now().Add(-time.Second)},
		{"checking", "checking", time.Time{}, time.Time{}},
		{"unverified", "unverified", time.Time{}, time.Time{}},
		{"review", "review", time.Time{}, time.Time{}},
		{"unavailable", "unavailable", time.Time{}, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t)
			if err := s.SetTarget("a/solo", true); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			target := s.state.Targets["a/solo"]
			target.State = tc.state
			target.CheckedAt = tc.checked
			target.AllowUntil = tc.allow
			err := s.saveLocked()
			s.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			assertGatePassThrough(t, s)
		})
	}
}
