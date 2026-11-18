package modelstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

func TestJSONEvidenceKeepsLargeIntegerPrecision(t *testing.T) {
	s := openTestStore(t)
	_, batch, sample, result, template, bank := seedRecords(t, s)
	large := json.Number("9007199254740993")
	sample.RequestParams["seed"] = large
	sample.RouteEvidence["sequence"] = large
	result.Metrics["exact_counter"] = large
	template.Parameters["seed"] = large
	bank.ValidationMetrics["exact_counter"] = large
	for _, err := range []error{s.PutSample(sample), s.PutResult(result), s.PutTemplate(template), s.PutBank(bank)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	samples, err := s.Samples(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, samples[0].RequestParams, sample.RequestParams)
	assertJSON(t, samples[0].RouteEvidence, sample.RouteEvidence)
	gotResult, err := s.Result(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, gotResult.Metrics, result.Metrics)
	templates, err := s.Templates()
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, templates[0].Parameters, template.Parameters)
	gotBank, err := s.Bank(bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, gotBank.ValidationMetrics, bank.ValidationMetrics)
	backup := exportTest(t, s)
	restored := openTestStore(t)
	if err = restored.Restore(backup); err != nil {
		t.Fatal(err)
	}
	restoredSamples, err := restored.Samples(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, restoredSamples[0].RequestParams, sample.RequestParams)
	assertJSON(t, restoredSamples[0].RouteEvidence["imported_unverified_evidence"], sample.RouteEvidence)
	restoredResult, err := restored.Result(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, restoredResult.Metrics["imported_unverified_metrics"], result.Metrics)
	restoredBank, err := restored.Bank(bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, restoredBank.ValidationMetrics["imported_unverified_metrics"], bank.ValidationMetrics)
	restoredTemplates, err := restored.Templates()
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, restoredTemplates[0].Parameters, template.Parameters)
}

func TestRestoreRejectsNullNonNullableFieldsWithValidNormalizedHash(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.PutTarget(Target{ID: "t", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	before := exportTest(t, s)
	for _, field := range []string{"enabled", "provider_id", "interval_minutes"} {
		t.Run(field, func(t *testing.T) {
			// json.Unmarshal normally coerces null into false/""/0, so the
			// normalized hash remains valid. Validation must reject that loss.
			bad := mutateExport(t, before, func(m map[string]any) {
				m["tables"].(map[string]any)["model_targets"].([]any)[0].(map[string]any)[field] = nil
			}, false)
			if err := s.Restore(bad); err == nil {
				t.Fatal("null primitive silently became its zero value")
			}
			if !bytes.Equal(before, exportTest(t, s)) {
				t.Fatal("null import changed persistent state")
			}
		})
	}
}

func TestCredentialHeaderVariantsNeverEnterPublicData(t *testing.T) {
	s := openTestStore(t)
	_, _, sample, _, _, _ := seedRecords(t, s)
	for _, key := range []string{"Authorization", "X-Api-Key", "Cookie", "Set-Cookie", "client_secret", "private_key"} {
		t.Run(key, func(t *testing.T) {
			sample.ID = "secret-" + key
			sample.RequestParams = map[string]any{"extra_headers": []any{map[string]any{key: "deterministic-test-secret"}}}
			if err := s.PutSample(sample); err == nil {
				t.Fatal("credential header variant persisted")
			}
		})
	}
}

func TestRestoreRejectsNullNumbersAndOptionalStrings(t *testing.T) {
	s := openTestStore(t)
	_, _, sample, _, _, _ := seedRecords(t, s)
	sample.ParsedNumbers, sample.Features = []int{0}, []float64{0}
	sample.SystemPromptHash, sample.RequestID = "", ""
	if err := s.PutSample(sample); err != nil {
		t.Fatal(err)
	}
	before := exportTest(t, s)
	for _, field := range []string{"parsed_numbers", "features", "system_prompt_hash", "request_id"} {
		t.Run(field, func(t *testing.T) {
			bad := mutateExport(t, before, func(m map[string]any) {
				row := m["tables"].(map[string]any)["fingerprint_samples"].([]any)[0].(map[string]any)
				if field == "parsed_numbers" || field == "features" {
					row[field] = []any{nil}
				} else {
					row[field] = nil
				}
			}, false)
			if err := s.Restore(bad); err == nil {
				t.Fatal("null array number/optional string silently became a zero value")
			}
			if !bytes.Equal(before, exportTest(t, s)) {
				t.Fatal("null evidence import changed storage")
			}
		})
	}
}

func TestBatchesSortByInstantNotTimezoneSpelling(t *testing.T) {
	s := openTestStore(t)
	target, _, _, _, _, _ := seedRecords(t, s)
	for _, batch := range []Batch{
		{ID: "older-local", TargetID: target.ID, StartedAt: "2026-11-18T00:00:00+08:00", Status: "INCONCLUSIVE"},
		{ID: "newer-utc", TargetID: target.ID, StartedAt: "2026-11-17T23:00:00Z", Status: "INCONCLUSIVE"},
	} {
		if err := s.PutBatch(batch); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.Batches(target.ID, 1)
	if err != nil || len(rows) != 1 || rows[0].ID != "newer-utc" {
		t.Fatalf("not sorted by timestamp instant: %v %v", rows, err)
	}
}

func TestRetargetingBatchCannotInvalidateExistingSamples(t *testing.T) {
	s := openTestStore(t)
	_, batch, _, _, _, _ := seedRecords(t, s)
	other, err := s.PutTarget(Target{ID: "other", ModelID: "other"})
	if err != nil {
		t.Fatal(err)
	}
	before := exportTest(t, s)
	batch.TargetID = other.ID
	if err = s.PutBatch(batch); err == nil {
		t.Fatal("batch retargeted while original samples still identify another target")
	}
	if !bytes.Equal(before, exportTest(t, s)) {
		t.Fatal("failed retarget changed data")
	}
}

func TestConcurrentActualChargesAndRestoresDoNotLoseDebt(t *testing.T) {
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
	settings.DailyTokens = 100
	if err = a.PutSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err = a.Reserve("2026-11-17", 1, 50); err != nil {
		t.Fatal(err)
	}
	backup := exportTest(t, a)
	var wg sync.WaitGroup
	errCh := make(chan error, 110)
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			if err := s.ChargeExtra("2026-11-17", 1); err != nil {
				errCh <- err
			}
		}(i)
	}
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.Restore(backup); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	u, err := a.Usage("2026-11-17")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, u, Usage{Day: "2026-11-17", Requests: 1, Tokens: 150})
	if err = b.Reserve("2026-11-17", 1, 0); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatal("concurrent restore lost actual-token debt", err)
	}
	t.Log("100 actual-token charges concurrent with 10 restores across two stores: no debt lost, final tokens=150/100")
}
