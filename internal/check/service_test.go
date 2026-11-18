package check

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) *Service {
	t.Helper()
	d := t.TempDir()
	gate := filepath.Join(d, "gate.js")
	s, e := New(d, gate)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s
}

// Exercise the published JavaScript, including the historical probe alias,
// without requiring Bun or skipping the compatibility contract on Windows.
func assertGatePassThrough(t *testing.T, s *Service) {
	t.Helper()
	code, err := os.ReadFile(s.gate)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	alias := s.state.Alias
	s.mu.Unlock()
	if strings.Contains(string(code), "a/solo") || strings.Contains(string(code), alias) {
		t.Error("compatibility entrypoint exposes target or alias metadata")
	}
	for _, retired := range []string{"const targets", "const alias", "ctx.reject", "MAGPIE_QUALITY_TRIGGER", "quality-probe/"} {
		if strings.Contains(string(code), retired) {
			t.Errorf("compatibility entrypoint retains retired logic: %s", retired)
		}
	}
	runtime, err := exec.LookPath("node")
	if err != nil {
		runtime, err = exec.LookPath("bun")
		if err != nil {
			t.Fatal("Node.js or Bun is required to verify the compatibility entrypoint")
		}
	}
	module := filepath.Join(t.TempDir(), "gate.mjs")
	if err := os.WriteFile(module, code, 0600); err != nil {
		t.Fatal(err)
	}
	script := `const {pathToFileURL}=require("node:url");
(async()=>{
 const m=await import(pathToFileURL(process.argv[1]).href);
 const bodies=[{model:"a/solo",messages:[{content:"ordinary request"}]},{model:"a/luna"},{model:"quality-probe/"+process.argv[2]+"/a/solo"},{model:17},{model:null},{model:{}},{},null,undefined];
 const log=console.log;let logged=0;console.log=()=>{logged++};
 for(const body of bodies){
  const before=JSON.stringify(body);
  const result=m.onRequest(body,{reject:()=>{throw Error("retired gate rejected a request")}});
  if(result!==body||JSON.stringify(body)!==before)throw Error("compatibility entrypoint changed a request");
 }
 if(logged!==0)throw Error("compatibility entrypoint emitted an automatic trigger");
 console.log=log;console.log("PASS_THROUGH");
})().catch(error=>{console.error(error);process.exit(1)});`
	if out, err := exec.Command(runtime, "-e", script, module, alias).CombinedOutput(); err != nil {
		t.Fatalf("pass-through contract: %s %v", out, err)
	} else if strings.TrimSpace(string(out)) != "PASS_THROUGH" {
		t.Fatalf("unexpected entrypoint output: %s", out)
	}
}

func TestGateAndPersistence(t *testing.T) {
	s := fixture(t)
	if e := s.SetTarget("a/solo", true); e != nil {
		t.Fatal(e)
	}
	assertGatePassThrough(t, s)
	if err := s.Action("a/solo", "hold"); err != nil {
		t.Fatal(err)
	}
	assertGatePassThrough(t, s)
	if err := s.Action("a/solo", "allow"); err != nil {
		t.Fatal(err)
	}
	assertGatePassThrough(t, s)
	state, _ := s.Snapshot()
	if !state.Targets["a/solo"].AllowUntil.After(time.Now()) {
		t.Fatal("no lease")
	}
	var restored State
	if e := s.store.Load(&restored); e != nil {
		t.Fatal(e)
	}
	if restored.Targets["a/solo"].State != "review" {
		t.Fatal("hold lost")
	}
	if state.Alias != "" || state.Config.GatewayKey != "" {
		t.Fatal("secret leaked")
	}
}
func TestGrade(t *testing.T) {
	for _, tt := range []struct{ a, w string }{{"Answer: **46.24 inches**.", "passed"}, {"46.24 inches is incorrect. Final: 48 inches.", "failed"}, {"48 inches", "failed"}, {"46.24", "passed"}, {"Example: 46.24, but my final answer is 48.", "failed"}} {
		if got := Grade("milk", tt.a); got != tt.w {
			t.Fatalf("%q: %s", tt.a, got)
		}
	}
}
func TestClient(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("auth missing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"46.24 inches"},"finish_reason":"stop"}],"usage":{"total_tokens":4}}`))
	}))
	defer up.Close()
	a, n, e := Call(context.Background(), Config{GatewayURL: up.URL, GatewayKey: "secret"}, "a/solo", "test")
	if e != nil || n != 4 || a != "46.24 inches" {
		t.Fatal(a, n, e)
	}
}
func TestHTTPAuthentication(t *testing.T) {
	s := fixture(t)
	h := s.Handler("secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/state", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("GET", "/api/state", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), s.state.Alias) {
		t.Fatal(w.Code)
	}
}
func TestTriggerDedupAndExpiry(t *testing.T) {
	s := fixture(t)
	s.SetTarget("a/solo", true)
	s.mu.Lock()
	s.state.Config = Config{GatewayURL: "http://127.0.0.1:1", GatewayKey: "secret", DailyLimit: 1}
	s.state.Targets["a/solo"].State = "passed"
	s.state.Targets["a/solo"].CheckedAt = time.Now()
	s.mu.Unlock()
	if e := s.Trigger("a/solo", "milk", false); e != nil {
		t.Fatal(e)
	}
	state, _ := s.Snapshot()
	if len(state.Results) != 0 {
		t.Fatal("fresh target probed")
	}
	s.LogLine(`MAGPIE_QUALITY_TRIGGER {"model":"a/luna"}`)
	state, _ = s.Snapshot()
	if len(state.Targets) != 1 {
		t.Fatal("luna configured automatically")
	}
}

func TestUnavailableCooldown(t *testing.T) {
	s := fixture(t)
	s.SetTarget("a/solo", true)
	s.mu.Lock()
	s.state.Config = Config{GatewayURL: "http://127.0.0.1:1", GatewayKey: "fixture", DailyLimit: 120}
	target := s.state.Targets["a/solo"]
	target.State = "unavailable"
	target.RetryAfter = time.Now().Add(15 * time.Minute)
	s.mu.Unlock()
	if err := s.Trigger("a/solo", "milk", false); err != nil {
		t.Fatal(err)
	}
	state, _ := s.Snapshot()
	if len(state.Results) != 0 || state.Targets["a/solo"].State != "unavailable" {
		t.Fatal("cooldown triggered paid probe")
	}
}
