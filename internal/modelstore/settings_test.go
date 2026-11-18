package modelstore

import (
	"bytes"
	"errors"
	"testing"
)

func TestSettingsResourceCapsAndZeroBudgetPause(t *testing.T) {
	s := openTestStore(t)
	maximum := Settings{DailyRequests: 1000000, DailyTokens: 1000000000, Concurrency: 8, TimeoutSeconds: 120, RetentionDays: 3650}
	if err := s.PutSettings(maximum); err != nil {
		t.Fatal("boundary caps rejected", err)
	}
	got, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, got, maximum)
	cases := []struct {
		name   string
		change func(*Settings)
	}{
		{"requests", func(s *Settings) { s.DailyRequests = 1000001 }},
		{"tokens", func(s *Settings) { s.DailyTokens = 1000000001 }},
		{"concurrency", func(s *Settings) { s.Concurrency = 9 }},
		{"timeout", func(s *Settings) { s.TimeoutSeconds = 121 }},
		{"retention", func(s *Settings) { s.RetentionDays = 3651 }},
	}
	before := exportTest(t, s)
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			bad := maximum
			test.change(&bad)
			if err := s.PutSettings(bad); !errors.Is(err, ErrInvalidData) {
				t.Fatalf("resource cap bypassed: %+v %v", bad, err)
			}
			if !bytes.Equal(before, exportTest(t, s)) {
				t.Fatal("rejected settings changed database")
			}
		})
	}
	paused := DefaultSettings()
	paused.DailyRequests, paused.DailyTokens = 0, 0
	if err = s.PutSettings(paused); err != nil {
		t.Fatal("zero budget should pause paid work", err)
	}
	if err = s.Reserve("2026-11-17", 1, 0); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatal("zero request limit did not pause", err)
	}
	if err = s.Reserve("2026-11-17", 0, 1); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatal("zero token limit did not pause", err)
	}
}

func TestRestoreCannotBypassSettingsResourceCaps(t *testing.T) {
	s := openTestStore(t)
	before := exportTest(t, s)
	cases := map[string]int{"daily_requests": 1000001, "daily_tokens": 1000000001, "concurrency": 9, "timeout_seconds": 121, "retention_days": 3651}
	for field, overLimit := range cases {
		t.Run(field, func(t *testing.T) {
			bad := mutateExport(t, before, func(m map[string]any) {
				m["tables"].(map[string]any)["store_settings"].(map[string]any)[field] = overLimit
			}, true)
			if err := s.Restore(bad); !errors.Is(err, ErrInvalidData) {
				t.Fatalf("restore bypassed %s cap: %v", field, err)
			}
			if !bytes.Equal(before, exportTest(t, s)) {
				t.Fatal("over-limit restore changed persistent state")
			}
		})
	}
}
