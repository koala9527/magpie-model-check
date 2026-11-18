package modelcheck

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/koala9527/magpie-model-check/internal/fingerprint"
	"github.com/koala9527/magpie-model-check/internal/modelstore"
)

func testService(t *testing.T, handler http.Handler) *Service {
	t.Helper()
	up := httptest.NewServer(handler)
	t.Cleanup(up.Close)
	s, err := New(t.TempDir(), Config{GatewayURL: up.URL, GatewayKey: "fixture-private-key", RouteKey: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func syntheticReply(w http.ResponseWriter, r *http.Request, authentic bool) {
	var request struct {
		Model  string `json:"model"`
		Marker struct {
			ID string `json:"id"`
		} `json:"_magpie_model_check_probe"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	json.NewDecoder(r.Body).Decode(&request)
	c := fingerprint.DefaultChallenges()[0]
	for _, challenge := range fingerprint.DefaultChallenges() {
		if len(request.Messages) > 0 && challenge.Prompt == request.Messages[0].Content {
			c = challenge
		}
	}
	values := make([]string, c.Count)
	for i := range values {
		values[i] = "17"
	}
	content := strings.Join(values, ",")
	reply := map[string]any{"model": "UNTRUSTED_SELF_CLAIM", "choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 20}}
	if authentic {
		reply["_magpie_model_check_route"] = signedRoute(strings.Repeat("a", 64), request.Marker.ID, request.Model, "channel", content, "stop")
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
}

func addTestTarget(t *testing.T, s *Service) modelstore.Target {
	t.Helper()
	target, err := s.store.PutTarget(modelstore.Target{ModelID: "channel/solo", ProviderID: "channel", ClaimedModel: "claimed", Enabled: true, RequestBudget: 1, IntervalMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	return target
}
func waitBatch(t *testing.T, s *Service, id string) modelstore.Result {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := s.store.Batch(id)
		if err == nil && b.CompletedAt != "" {
			result, err := s.store.Result(id)
			if err != nil {
				t.Fatal(err)
			}
			return result
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("batch did not finish")
	return modelstore.Result{}
}
func TestAuthenticatedProbePersistsRawSampleButDoesNotInventIdentity(t *testing.T) {
	s := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { syntheticReply(w, r, true) }))
	target := addTestTarget(t, s)
	id, err := s.StartCheck(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := waitBatch(t, s, id)
	claim, ok := result.Metrics["claim_snapshot"].(map[string]any)
	if !ok || claim["model_id"] != target.ModelID || claim["provider_id"] != target.ProviderID || claim["claimed_model"] != target.ClaimedModel {
		t.Fatal("report did not retain the exact target declaration")
	}
	if result.IdentityStatus != "INCONCLUSIVE" || result.CalibratedProbability != nil || result.PredictedModel != "" {
		t.Fatalf("unvalidated identity: %+v", result)
	}
	samples, err := s.store.Samples(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || !samples[0].Valid || samples[0].RawResponse == "" || samples[0].RouteEvidence["authenticated"] != true {
		t.Fatalf("incomplete sample: %+v", samples)
	}
	if samples[0].ResponseModelField != "UNTRUSTED_SELF_CLAIM" {
		t.Fatal("lost self-reported field")
	}
	backup, err := s.store.Export()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(backup), "fixture-private-key") || strings.Contains(string(backup), strings.Repeat("a", 64)) {
		t.Fatal("credentials exported")
	}
}
func TestSpoofedPublicRouteIsUnverifiedNotMismatched(t *testing.T) {
	s := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { syntheticReply(w, r, false) }))
	target := addTestTarget(t, s)
	id, err := s.StartCheck(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result := waitBatch(t, s, id); result.IdentityStatus != "ROUTE_UNVERIFIED" {
		t.Fatal(result)
	}
}
func TestCancellationStopsInflightIOAndReleasesTarget(t *testing.T) {
	entered, closed := make(chan struct{}), make(chan struct{})
	s := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(closed)
	}))
	target := addTestTarget(t, s)
	id, err := s.StartCheck(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	if _, err = s.StartCheck(target.ID); err == nil {
		t.Fatal("duplicate target job accepted")
	}
	s.CancelCheck(id)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancel did not close upstream request")
	}
	if result := waitBatch(t, s, id); result.IdentityStatus != "UNAVAILABLE" || result.RejectionReason != "CANCELLED" {
		t.Fatal(result)
	}
}
func TestDailyBudgetIsReservedBeforeAnyUpstreamRequest(t *testing.T) {
	var requests atomic.Int32
	s := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); syntheticReply(w, r, true) }))
	target := addTestTarget(t, s)
	settings, _ := s.store.Settings()
	settings.DailyRequests = 1
	settings.DailyTokens = 100000
	s.store.PutSettings(settings)
	id, err := s.StartCheck(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitBatch(t, s, id)
	target, _ = s.store.Target(target.ID)
	target.RetryAfter = ""
	s.store.PutTarget(target)
	id, err = s.StartCheck(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result := waitBatch(t, s, id); result.IdentityStatus != "UNAVAILABLE" || result.RejectionReason != "BUDGET_EXHAUSTED" {
		t.Fatal(result)
	}
	if requests.Load() != 1 {
		t.Fatal("spent beyond request budget", requests.Load())
	}
}

func TestLoopbackGatewayKeepsTheConfiguredPort(t *testing.T) {
	got, err := localGateway("http://localhost:55123/v1")
	if err != nil || got != "http://127.0.0.1:55123" {
		t.Fatalf("lost configured gateway port: %q %v", got, err)
	}
}

func TestKnownCredentialsAreRedactedBeforePersistence(t *testing.T) {
	s := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model  string `json:"model"`
			Marker struct {
				ID string `json:"id"`
			} `json:"_magpie_model_check_probe"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		text := "fixture-private-key"
		json.NewEncoder(w).Encode(map[string]any{"model": text, "choices": []any{map[string]any{"message": map[string]string{"content": text}, "finish_reason": "stop"}}, "_magpie_model_check_route": signedRoute(strings.Repeat("a", 64), request.Marker.ID, request.Model, "channel", text, "stop")})
	}))
	target := addTestTarget(t, s)
	id, err := s.StartCheck(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitBatch(t, s, id)
	data, err := s.store.Export()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "fixture-private-key") {
		t.Fatal("upstream credential echo entered local backup")
	}
}
func TestNetworkErrorsDoNotBecomeModelReplacement(t *testing.T) {
	s := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte("Authorization: fixture-private-key"))
	}))
	target := addTestTarget(t, s)
	id, err := s.StartCheck(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := waitBatch(t, s, id)
	if result.IdentityStatus != "UNAVAILABLE" || result.RejectionReason != "HTTP_429" {
		t.Fatal(result)
	}
	if strings.Contains(result.RejectionReason, "fixture-private-key") {
		t.Fatal("error echoed credential")
	}
}
