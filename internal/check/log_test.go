package check

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestLogDemandAndRotation(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"choices":[{"message":{"content":"46.24 inches"},"finish_reason":"stop"}]}`))
	}))
	defer up.Close()
	s := fixture(t)
	log := filepath.Join(t.TempDir(), "magpie.log")
	marker := []byte("MAGPIE_QUALITY_TRIGGER {\"model\":\"a/solo\"}\n")
	if err := os.WriteFile(log, marker, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(Config{GatewayURL: up.URL, GatewayKey: "fixture", LogPath: log, DailyLimit: 120}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTarget("a/solo", true); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Snapshot()
	s.WatchLog()
	time.Sleep(1200 * time.Millisecond)
	assertNoRequests := func() {
		t.Helper()
		waitIdle(t, s)
		after, _ := s.Snapshot()
		if calls.Load() != 0 || !reflect.DeepEqual(before, after) {
			t.Fatalf("retired log watcher changed state or issued requests: calls=%d", calls.Load())
		}
		s.mu.Lock()
		lastActivity := s.lastActivity
		s.mu.Unlock()
		if !lastActivity.IsZero() {
			t.Fatal("retired log watcher recorded automatic activity")
		}
	}
	assertNoRequests()
	appendTrigger := func() {
		f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := f.Write(marker)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal(writeErr, closeErr)
		}
	}
	appendTrigger()
	time.Sleep(1200 * time.Millisecond)
	assertNoRequests()
	// Rotation must work while the service is open, especially on Windows:
	// the retired watcher must not retain a handle on either log generation.
	if err := os.Rename(log, log+".old"); err != nil {
		t.Fatal(err)
	}
	if old, err := os.ReadFile(log + ".old"); err != nil || string(old) != string(marker)+string(marker) {
		t.Fatal("rotation lost the original log contents", err)
	}
	if err := os.WriteFile(log, marker, 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	assertNoRequests()
	if err := os.Rename(log, log+".rotated"); err != nil {
		t.Fatal("service retained the replacement log", err)
	}
	if rotated, err := os.ReadFile(log + ".rotated"); err != nil || string(rotated) != string(marker) {
		t.Fatal("rotation lost the replacement log contents", err)
	}
	if err := os.WriteFile(log, marker, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(log, 0); err != nil {
		t.Fatal(err)
	}
	appendTrigger()
	time.Sleep(1200 * time.Millisecond)
	assertNoRequests()
}

func TestLogLineDoesNotTriggerQualityChecks(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"choices":[{"message":{"content":"46.24 inches"},"finish_reason":"stop"}]}`))
	}))
	defer up.Close()
	s := fixture(t)
	if err := s.Configure(Config{GatewayURL: up.URL, GatewayKey: "fixture", DailyLimit: 120}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTarget("a/solo", true); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Snapshot()
	for _, line := range []string{
		`ordinary log line`,
		`MAGPIE_QUALITY_TRIGGER not-json`,
		`MAGPIE_QUALITY_TRIGGER {"model":"a/luna"}`,
		`middleware quality-check [info]: MAGPIE_QUALITY_TRIGGER {"model":"a/solo"}`,
		`MAGPIE_QUALITY_TRIGGER {"model":"a/solo"}`,
	} {
		s.LogLine(line)
	}
	waitIdle(t, s)
	after, _ := s.Snapshot()
	if calls.Load() != 0 || !reflect.DeepEqual(before, after) {
		t.Fatalf("log lines automatically issued requests or changed historical state: calls=%d", calls.Load())
	}
	s.mu.Lock()
	lastActivity := s.lastActivity
	s.mu.Unlock()
	if !lastActivity.IsZero() {
		t.Fatal("log lines recorded automatic activity")
	}
}
