package check

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestartRestoresProtection(t *testing.T) {
	dir := t.TempDir()
	gate := filepath.Join(dir, "gate.js")
	s, err := New(dir, gate)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTarget("a/solo", true); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.Action("a/solo", "hold"); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.Action("a/solo", "allow"); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.mu.Lock()
	s.state.Targets["a/solo"].AllowUntil = time.Now().Add(-time.Minute)
	alias := s.state.Alias
	err = s.saveLocked()
	s.mu.Unlock()
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an entrypoint left by the pre-migration service.
	if err := os.WriteFile(gate, []byte(`export function onRequest(body,ctx){return ctx.reject(403,"legacy gate");}`), 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := New(dir, gate)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	state, _ := restored.Snapshot()
	if state.Targets["a/solo"].State != "review" || !state.Targets["a/solo"].Enabled || state.Targets["a/solo"].AllowUntil.After(time.Now()) {
		t.Fatal("restart lost historical target state")
	}
	if restored.state.Alias != alias {
		t.Fatal("restart changed the historical manual-tool alias")
	}
	assertGatePassThrough(t, restored)
}
