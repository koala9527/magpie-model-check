package modelcheck

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/koala9527/magpie-model-check/internal/fingerprint"
	"github.com/koala9527/magpie-model-check/internal/modelstore"
)

func management(t *testing.T, s *Service) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	server.Config.Handler = s.Handler("control-private", "page-session", origin)
	server.Start()
	t.Cleanup(server.Close)
	return server
}
func apiRequest(t *testing.T, server *httptest.Server, method, path, body, token, origin string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Body.Close() })
	return r
}
func TestPageAPIRequiresSessionAndRejectsForeignOrigin(t *testing.T) {
	s := testService(t, http.NotFoundHandler())
	server := management(t, s)
	for _, tc := range []struct {
		token, origin string
		want          int
	}{{"", "", 401}, {"page-session", "https://attacker.example", 403}, {"page-session", "null", 200}} {
		r := apiRequest(t, server, "GET", "/api/state", "", tc.token, tc.origin)
		if r.StatusCode != tc.want {
			t.Fatalf("%s %s => %d", tc.token, tc.origin, r.StatusCode)
		}
		if r.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("missing defensive headers")
		}
	}
	r := apiRequest(t, server, "GET", "/", "", "", "null")
	if r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("bootstrap HTML readable by an arbitrary opaque origin")
	}
	body, _ := io.ReadAll(r.Body)
	if !bytes.Contains(body, []byte("model-check-session")) || bytes.Contains(body, []byte("fixture-private-key")) {
		t.Fatal("invalid page boot data")
	}
}
func TestTargetCRUDAndManagedResultFields(t *testing.T) {
	s := testService(t, http.NotFoundHandler())
	server := management(t, s)
	r := apiRequest(t, server, "POST", "/api/targets", `{"model_id":"channel/solo","provider_id":"channel","claimed_model":"A","enabled":true,"auto_check":false,"last_status":"MATCHED"}`, "page-session", "null")
	if r.StatusCode != 201 {
		b, _ := io.ReadAll(r.Body)
		t.Fatal(r.StatusCode, string(b))
	}
	var target map[string]any
	json.NewDecoder(r.Body).Decode(&target)
	if target["last_status"] != "PENDING" || target["auto_check"] != false {
		t.Fatal("user supplied an attribution status", target)
	}
	r = apiRequest(t, server, "POST", "/api/targets", `{"model_id":"solo","claimed_model":"A","enabled":true,"auto_check":true}`, "page-session", "null")
	if r.StatusCode != 422 {
		t.Fatal("unbound scheduled requests accepted", r.StatusCode)
	}
}
func TestBackupIsAuthenticatedAndRestoreRejectsInvalidJSON(t *testing.T) {
	s := testService(t, http.NotFoundHandler())
	server := management(t, s)
	r := apiRequest(t, server, "GET", "/api/backup", "", "page-session", "null")
	body, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || !json.Valid(body) {
		t.Fatal(r.StatusCode, string(body))
	}
	if strings.Contains(string(body), "control-private") || strings.Contains(string(body), "fixture-private-key") {
		t.Fatal("private material exported")
	}
	r = apiRequest(t, server, "POST", "/api/restore", `{"schema":999}`, "page-session", "null")
	if r.StatusCode != 422 {
		t.Fatal("invalid backup restored", r.StatusCode)
	}
}

func TestDefaultShortGatewayKeyDoesNotMatchProductNamesDuringBackup(t *testing.T) {
	s := testService(t, http.NotFoundHandler())
	s.config.GatewayKey = "magpie"
	server := management(t, s)
	r := apiRequest(t, server, "GET", "/api/backup", "", "page-session", "null")
	if r.StatusCode != 200 {
		t.Fatal("product name was mistaken for a credential", r.StatusCode)
	}
	if !s.containsPrivateValue([]byte(`{"content":"Bearer magpie"}`)) {
		t.Fatal("actual short credential was missed")
	}
}

func TestBankExportImportPreservesHashedRawJSON(t *testing.T) {
	s := testService(t, http.NotFoundHandler())
	server := management(t, s)
	raw := json.RawMessage("{\n  \"name\": \"测试 <reference>\", \"counts\": [1, 2]\n}\n")
	err := s.store.PutBank(modelstore.Bank{ID: "raw-bank", Version: "test-v1", AlgorithmVersion: fingerprint.AlgorithmVersion, ChallengeVersion: ChallengeVersion, SourceType: "manual_import", Provenance: "Synthetic formatting regression fixture", Data: raw})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.store.Bank("raw-bank")
	if err != nil {
		t.Fatal(err)
	}
	r := apiRequest(t, server, "GET", "/api/banks/raw-bank/export", "", "page-session", "null")
	var exported map[string]any
	if json.NewDecoder(r.Body).Decode(&exported) != nil || r.StatusCode != 200 {
		t.Fatal("export failed")
	}
	data, ok := exported["data"].(string)
	if !ok || data != string(raw) {
		t.Fatalf("hashed raw JSON changed during export: %T %q", exported["data"], data)
	}
	if exported["sha256"] != stored.SHA256 {
		t.Fatal("export digest changed")
	}
	exported["id"] = "roundtrip-bank"
	exported["trusted"] = true
	encoded, _ := json.Marshal(exported)
	r = apiRequest(t, server, "POST", "/api/banks", string(encoded), "page-session", "null")
	if r.StatusCode != 201 {
		b, _ := io.ReadAll(r.Body)
		t.Fatal(r.StatusCode, string(b))
	}
	reimported, err := s.store.Bank("roundtrip-bank")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reimported.Data, raw) || reimported.SHA256 != stored.SHA256 || reimported.Trusted {
		t.Fatal("roundtrip lost bytes or trusted an imported flag")
	}
}

func TestBuiltinBankExportsThePinnedReferenceNotPlaceholder(t *testing.T) {
	s := testService(t, http.NotFoundHandler())
	bank, err := s.store.Bank("builtin-modeltrace")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bank.Data, fingerprint.LegacyBankJSON()) || bank.SHA256 != fingerprint.LegacyBankSHA256() {
		t.Fatal("builtin bank is not the actual pinned reference")
	}
	if bank.Trusted {
		t.Fatal("historical centroids became validated evidence")
	}
}

func TestTargetCreationSerializesWithServiceMutations(t *testing.T) {
	s := testService(t, http.NotFoundHandler())
	handler := s.Handler("control-private", "page-session", "http://127.0.0.1:12345")
	req := httptest.NewRequest("POST", "http://127.0.0.1:12345/api/targets", strings.NewReader(`{"model_id":"channel/solo","claimed_model":"A","enabled":true}`))
	req.Header.Set("Authorization", "Bearer page-session")
	recorder := httptest.NewRecorder()
	s.mu.Lock()
	done := make(chan struct{})
	go func() { handler.ServeHTTP(recorder, req); close(done) }()
	early := false
	select {
	case <-done:
		early = true
	case <-time.After(100 * time.Millisecond):
	}
	s.mu.Unlock()
	if early {
		t.Fatal("target creation bypassed the service mutation lock")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("creation did not resume")
	}
	if recorder.Code != 201 {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
}

func TestBusyTargetBlocksUpdateDeleteAndRestore(t *testing.T) {
	s := testService(t, http.NotFoundHandler())
	server := management(t, s)
	target := addTestTarget(t, s)
	backup, err := s.store.Export()
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.active[target.ID] = job{id: "persisting-batch", cancel: func() {}}
	s.mu.Unlock()
	for _, operation := range []struct{ method, path, body string }{
		{"PUT", "/api/targets/" + target.ID, `{"model_id":"channel/other","claimed_model":"B","enabled":true}`},
		{"DELETE", "/api/targets/" + target.ID, ""},
		{"POST", "/api/restore", string(backup)},
	} {
		r := apiRequest(t, server, operation.method, operation.path, operation.body, "page-session", "null")
		if r.StatusCode != 409 {
			t.Fatal(operation.method, operation.path, r.StatusCode)
		}
	}
	stored, err := s.store.Target(target.ID)
	if err != nil || stored.ModelID != target.ModelID {
		t.Fatal("busy target was mutated", err)
	}
	s.mu.Lock()
	delete(s.active, target.ID)
	s.mu.Unlock()
}

func TestChangingDeclaredFamilyOrVersionRequiresRecheck(t *testing.T) {
	s := testService(t, http.NotFoundHandler())
	server := management(t, s)
	for _, field := range []string{"expected_family", "expected_version"} {
		target := addTestTarget(t, s)
		target.LastStatus = "MATCHED"
		target.LastCheckedAt = nowText()
		target, err := s.store.PutTarget(target)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(target)
		var update map[string]any
		json.Unmarshal(data, &update)
		update[field] = "new-declaration"
		data, _ = json.Marshal(update)
		r := apiRequest(t, server, "PUT", "/api/targets/"+target.ID, string(data), "page-session", "null")
		if r.StatusCode != 200 {
			t.Fatal(r.StatusCode)
		}
		after, err := s.store.Target(target.ID)
		if err != nil || after.LastStatus != "PENDING" || after.LastCheckedAt != "" {
			t.Fatal(field, "kept an obsolete attribution", after, err)
		}
	}
}
