package modelstore

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertJSON(t *testing.T, got, want any) {
	t.Helper()
	if !bytes.Equal(mustJSON(t, got), mustJSON(t, want)) {
		t.Fatalf("got %s; want %s", mustJSON(t, got), mustJSON(t, want))
	}
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func seedRecords(t *testing.T, s *Store) (Target, Batch, Sample, Result, Template, Bank) {
	t.Helper()
	stamp := "2026-11-17T08:00:00Z"
	target, err := s.PutTarget(Target{ID: "target-a", ProviderID: "provider-a", ModelID: "model-a", ClaimedModel: "claim-a", ExpectedFamily: "family-a", ExpectedVersion: "v1", Enabled: true, IntervalMinutes: 30, RequestBudget: 3, LastStatus: "INCONCLUSIVE", LastCheckedAt: stamp, RetryAfter: stamp})
	if err != nil {
		t.Fatal(err)
	}
	batch := Batch{ID: "batch-a", TargetID: target.ID, ClaimedModel: target.ClaimedModel, AlgorithmVersion: "test-algorithm", ChallengeVersion: "test-challenge", BankVersion: "test-bank", Status: "INCONCLUSIVE", RouteBindingStatus: "ROUTE_UNVERIFIED", StartedAt: stamp, CompletedAt: stamp, ErrorMessage: "unit-test fixture", ValidSamples: 1}
	if err = s.PutBatch(batch); err != nil {
		t.Fatal(err)
	}
	sample := Sample{ID: "sample-a", TargetID: target.ID, BatchID: batch.ID, SourceType: "unit_test", ChallengeID: "challenge-a", ParserVersion: "test-parser", RawResponse: "1 2 3\n", ResponseModelField: "untrusted-claim", CollectedAt: stamp, ChallengeIndex: 1, LatencyMS: 12, InputTokens: 5, OutputTokens: 8, Valid: true, ParsedNumbers: []int{1, 2, 3}, Features: []float64{0.125, 0.5}, RequestParams: map[string]any{"temperature": 0.2, "stream": false}, RouteEvidence: map[string]any{"verified": false, "provider_id": "provider-a"}}
	sample.ResponseSHA256 = digest([]byte(sample.RawResponse))
	sample.SystemPromptHash = digest(nil)
	sample.RequestID = "request-a"
	if err = s.PutSample(sample); err != nil {
		t.Fatal(err)
	}
	top1, top2, margin := 0.5, 0.4, 0.1
	result := Result{ID: "result-a", BatchID: batch.ID, ExpectedModel: "claim-a", PredictedModel: "model-a", PredictedFamily: "family-a", IdentityStatus: "INCONCLUSIVE", RejectionReason: "test fixtures are not real reference evidence", BankVersion: "test-bank", AlgorithmVersion: "test-algorithm", CreatedAt: stamp, Top1Score: &top1, Top2Score: &top2, ScoreMargin: &margin, Candidates: []map[string]any{{"model": "model-a", "score": 0.5}}, Metrics: map[string]any{"calibrated": false}}
	if err = s.PutResult(result); err != nil {
		t.Fatal(err)
	}
	template := Template{ID: "template-a", Name: "unit-test fixture", Version: 1, Prompt: "Return three integers.", ParserVersion: "test-parser", CreatedAt: stamp, Parameters: map[string]any{"count": 3}}
	template.PromptSHA256 = digest([]byte(template.Prompt))
	if err = s.PutTemplate(template); err != nil {
		t.Fatal(err)
	}
	bank := Bank{ID: "bank-a", Version: "test-bank", AlgorithmVersion: "test-algorithm", ChallengeVersion: "test-challenge", SourceType: "unit_test", Provenance: "deterministic test fixture; not reference evidence", CreatedAt: stamp, ModelCount: 0, SampleCount: 0, Data: json.RawMessage("{\n  \"test_fixture\": true\n}\n"), ValidationMetrics: map[string]any{"validated": false}}
	bank.SHA256 = digest(bank.Data)
	if err = s.PutBank(bank); err != nil {
		t.Fatal(err)
	}
	return target, batch, sample, result, template, bank
}

func TestOpenDefaultsSchemaAndTargetCRUD(t *testing.T) {
	s := openTestStore(t)
	settings, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, settings, DefaultSettings())
	var version int
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("schema version %d: %v", version, err)
	}
	for _, table := range []string{"model_targets", "challenge_templates", "fingerprint_samples", "fingerprint_batches", "attribution_results", "reference_banks"} {
		var count int
		if err = s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("missing table %s: %v", table, err)
		}
	}
	target, err := s.PutTarget(Target{ModelID: "provider/model", Enabled: true})
	if err != nil || target.ID == "" || target.AutoCheck || target.CreatedAt == "" || target.UpdatedAt == "" || target.LastStatus == "MATCHED" {
		t.Fatalf("target %#v: %v", target, err)
	}
	created := target.CreatedAt
	target.ClaimedModel = "new-claim"
	target.CreatedAt = "2000-01-01T00:00:00Z"
	target, err = s.PutTarget(target)
	if err != nil || target.CreatedAt != created {
		t.Fatalf("update changed creation time: %#v, %v", target, err)
	}
	got, err := s.Target(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, got, target)
	all, err := s.Targets()
	if err != nil || len(all) != 1 {
		t.Fatalf("targets: %v, %v", all, err)
	}
	if err = s.DeleteTarget(target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Target(target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Target missing: %v", err)
	}
	if err = s.DeleteTarget(target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteTarget missing: %v", err)
	}
}

func TestEveryRecordPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	target, batch, sample, result, template, bank := seedRecords(t, s)
	if err = s.Reserve("2026-11-17", 2, 9000); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	gotTarget, err := s.Target(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, gotTarget, target)
	gotBatch, err := s.Batch(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, gotBatch, batch)
	allBatches, err := s.Batches(target.ID, 1)
	if err != nil || len(allBatches) != 1 {
		t.Fatalf("Batches: %v %v", allBatches, err)
	}
	gotSamples, err := s.Samples(batch.ID)
	if err != nil || len(gotSamples) != 1 {
		t.Fatalf("Samples: %v %v", gotSamples, err)
	}
	assertJSON(t, gotSamples[0], sample)
	gotResult, err := s.Result(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, gotResult, result)
	if gotResult.CalibratedProbability != nil {
		t.Fatal("invented calibrated probability")
	}
	gotTemplates, err := s.Templates()
	if err != nil || len(gotTemplates) != 1 {
		t.Fatalf("Templates: %v %v", gotTemplates, err)
	}
	assertJSON(t, gotTemplates[0], template)
	gotBank, err := s.Bank(bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, gotBank, bank)
	if !bytes.Equal(gotBank.Data, bank.Data) || digest(gotBank.Data) != bank.SHA256 {
		t.Fatal("bank raw bytes/hash changed")
	}
	gotBanks, err := s.Banks()
	if err != nil || len(gotBanks) != 1 {
		t.Fatalf("Banks: %v %v", gotBanks, err)
	}
	u, err := s.Usage("2026-11-17")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, u, Usage{Day: "2026-11-17", Requests: 2, Tokens: 9000})
}

func TestMissingRecordsAndInvalidWrites(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Batch("absent"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Result("absent"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Bank("absent"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.DeleteBank("absent"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.PutTarget(Target{}); err == nil {
		t.Fatal("empty model accepted")
	}
	if err := s.PutBatch(Batch{ID: "b", TargetID: "absent"}); err == nil {
		t.Fatal("orphan batch accepted")
	}
	if err := s.PutSample(Sample{ID: "s", BatchID: "absent"}); err == nil {
		t.Fatal("orphan sample accepted")
	}
	if err := s.PutResult(Result{ID: "r", BatchID: "absent"}); err == nil {
		t.Fatal("orphan result accepted")
	}
	if err := s.PutBank(Bank{ID: "bank", Data: json.RawMessage(`{"x":1}`), SHA256: "bad"}); err == nil {
		t.Fatal("bad bank hash accepted")
	}
	if err := s.PutTemplate(Template{ID: "t", Prompt: "x", PromptSHA256: "bad"}); err == nil {
		t.Fatal("bad prompt hash accepted")
	}
	_, batch, _, _, _, bank := seedRecords(t, s)
	nan := math.NaN()
	if err := s.PutResult(Result{ID: "nan", BatchID: batch.ID, Top1Score: &nan}); err == nil {
		t.Fatal("NaN score accepted")
	}
	if err := s.PutSample(Sample{ID: "secret", BatchID: batch.ID, RequestParams: map[string]any{"headers": map[string]any{"Authorization": "test-secret"}}}); err == nil {
		t.Fatal("request credential entered page-readable storage")
	}
	if err := s.DeleteBank(bank.ID); err != nil {
		t.Fatal(err)
	}
}

func TestReserveAtomicAcrossStoresAndCurrentSettings(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	settings := DefaultSettings()
	settings.DailyRequests, settings.DailyTokens = 17, 1700
	if err = a.PutSettings(settings); err != nil {
		t.Fatal(err)
	}
	var success atomic.Int64
	var wg sync.WaitGroup
	errCh := make(chan error, 100)
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			err := s.Reserve("2026-11-17", 1, 100)
			if err == nil {
				success.Add(1)
			} else if !errors.Is(err, ErrBudgetExceeded) {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if success.Load() != 17 {
		t.Fatalf("success=%d, want 17", success.Load())
	}
	u, err := b.Usage("2026-11-17")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, u, Usage{Day: "2026-11-17", Requests: 17, Tokens: 1700})
	settings.DailyRequests, settings.DailyTokens = 18, 1800
	if err = b.PutSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err = a.Reserve("2026-11-17", 1, 100); err != nil {
		t.Fatalf("cached settings used: %v", err)
	}
	before, _ := a.Usage("2026-11-17")
	for _, in := range []struct {
		day              string
		requests, tokens int
	}{{"not-a-day", 1, 1}, {"2026-11-17", -1, 0}, {"2026-11-17", 0, -1}, {"2026-11-17", int(^uint(0) >> 1), 0}} {
		if err = a.Reserve(in.day, in.requests, in.tokens); err == nil {
			t.Fatalf("invalid/overbudget reservation accepted: %+v", in)
		}
	}
	after, _ := a.Usage("2026-11-17")
	assertJSON(t, after, before)
	t.Logf("100 concurrent reservations across two connections: %d accepted, 83 rejected; ledger=%+v", success.Load(), u)
}

func TestReservationFailureRollsBackBothCounters(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec(`CREATE TRIGGER abort_usage BEFORE INSERT ON daily_usage BEGIN SELECT RAISE(ABORT,'test interruption'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Reserve("2026-11-17", 1, 100); err == nil {
		t.Fatal("interrupted reservation succeeded")
	}
	u, err := s.Usage("2026-11-17")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, u, Usage{Day: "2026-11-17"})
	if _, err = s.db.Exec("DROP TRIGGER abort_usage"); err != nil {
		t.Fatal(err)
	}
	if err = s.Reserve("2026-11-17", 1, 100); err != nil {
		t.Fatal(err)
	}
}

func TestChargeExtraPersistsDebtAndNeverRefunds(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	settings := DefaultSettings()
	settings.DailyRequests, settings.DailyTokens = 2, 100
	if err = s.PutSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err = s.Reserve("2026-11-17", 1, 100); err != nil {
		t.Fatal(err)
	}
	beforeDebt := exportTest(t, s)
	if err = s.ChargeExtra("2026-11-17", 31); err != nil {
		t.Fatalf("extra actual tokens rejected at exhausted budget: %v", err)
	}
	if err = s.Reserve("2026-11-17", 1, 0); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("token debt did not block following reservation: %v", err)
	}
	if err = s.ChargeExtra("2026-11-17", -1); err == nil {
		t.Fatal("negative extra charge refunded debt")
	}
	if err = s.ChargeExtra("bad-day", 1); err == nil {
		t.Fatal("invalid debt day accepted")
	}
	if err = s.ChargeExtra("2026-11-17", int(^uint(0)>>1)); err == nil {
		t.Fatal("extra charge integer overflow accepted")
	}
	_ = s.Close()
	s, err = Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Restore(beforeDebt); err != nil {
		t.Fatal(err)
	}
	u, err := s.Usage("2026-11-17")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, u, Usage{Day: "2026-11-17", Requests: 1, Tokens: 131})
	if err = s.Reserve("2026-11-17", 1, 0); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatal("restart/backup restore reset actual-token debt", err)
	}
	t.Log("reserved=100; actual extra=31; persisted tokens=131 over limit=100; restart/restore kept debt; later Reserve rejected")
}

func TestPruneAndDeleteNeverRefundReservations(t *testing.T) {
	s := openTestStore(t)
	target, batch, sample, result, _, bank := seedRecords(t, s)
	old := time.Now().UTC().AddDate(0, 0, -90).Format(time.RFC3339Nano)
	batch.StartedAt, batch.CompletedAt = old, old
	sample.CollectedAt, result.CreatedAt = old, old
	for _, err := range []error{s.PutBatch(batch), s.PutSample(sample), s.PutResult(result), s.Reserve("2026-11-17", 60, 300000)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	active := Batch{ID: "active", TargetID: target.ID, StartedAt: old, Status: "RUNNING"}
	if err := s.PutBatch(active); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(30); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Batch(batch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old completed history retained: %v", err)
	}
	if _, err := s.Batch(active.ID); err != nil {
		t.Fatalf("active job pruned: %v", err)
	}
	if _, err := s.Target(target.ID); err != nil {
		t.Fatal("target pruned", err)
	}
	if _, err := s.Bank(bank.ID); err != nil {
		t.Fatal("bank pruned", err)
	}
	if err := s.DeleteTarget(target.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBank(bank.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Reserve("2026-11-17", 1, 0); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatal("history deletion reset budget", err)
	}
	u, err := s.Usage("2026-11-17")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, u, Usage{Day: "2026-11-17", Requests: 60, Tokens: 300000})
	if err := s.Prune(0); err == nil {
		t.Fatal("nonpositive retention accepted")
	}
}

func TestRejectFutureSchemaWithoutChangingIt(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if s, err = Open(dir, ""); err == nil {
		_ = s.Close()
		t.Fatal("future schema silently downgraded")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, DatabaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion+1 {
		t.Fatalf("future schema modified on rejection: version=%d err=%v", version, err)
	}
}
