package modelcheck

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/koala9527/magpie-model-check/internal/attribution"
	"github.com/koala9527/magpie-model-check/internal/modelstore"
)

func TestDateReviewRoundAdmission(t *testing.T) {
	transfer, _ := admissionFixture(t, attribution.Real)
	bank := transfer.Bank
	bank.Data, bank.SHA256 = json.RawMessage(transfer.Data), digestBytes([]byte(transfer.Data))
	entry, err := loadReference(bank, attribution.Options{
		Policy: &attribution.Policy{Confidence: .95, MaxFalseMatchRate: 1, MaxFalseReplacementRate: 1,
			MinUnknownRecall: 0, MaxKnownRejectionRate: 1, MinTop1Accuracy: 0, MinFamilyAccuracy: 0},
		VerifyProvenance: func(attribution.Binding) error { return nil },
	})
	if err != nil || !entry.report.IdentityValidated || !entry.compatible {
		t.Fatal("generated trusted adapter fixture did not validate", err)
	}
	var corpus attribution.Corpus
	if err := json.Unmarshal(entry.engine.RawCorpusJSON(), &corpus); err != nil {
		t.Fatal(err)
	}
	var day, otherDay time.Time
	for _, record := range corpus.Records {
		if record.Channel == "channel" && record.Split == attribution.Test {
			day, err = time.Parse(time.RFC3339Nano, record.CollectedAt)
		}
		if record.Channel == "channel" && record.Split == attribution.TimeHoldout {
			otherDay, err = time.Parse(time.RFC3339Nano, record.CollectedAt)
		}
	}
	if err != nil || day.IsZero() || otherDay.IsZero() {
		t.Fatal("validated fixture days missing", err)
	}
	day = day.UTC().Truncate(24 * time.Hour)
	otherDay = otherDay.UTC().Truncate(24 * time.Hour)
	challenges := collectionChallenges()
	newSamples := func() []modelstore.Sample {
		var samples []modelstore.Sample
		for i := 0; i < 2*len(challenges); i++ {
			ch := challenges[i%len(challenges)]
			samples = append(samples, modelstore.Sample{ChallengeID: ch.ID,
				RawResponse:   strings.TrimSuffix(strings.Repeat("17,", ch.Count), ","),
				CollectedAt:   day.Add(10*time.Hour + time.Duration(i)*time.Second).Format(time.RFC3339Nano),
				RequestParams: map[string]any{"parameters": ch.Parameters, "finish_reason": "stop"},
				RouteEvidence: map[string]any{"authenticated": true, "protocol_verified": true, "provider_id": "channel", "provider_model": "solo"}})
		}
		return samples
	}
	for _, tc := range []struct {
		name, reason string
		change       func([]modelstore.Sample)
	}{
		{name: "trusted_complete_rounds"},
		{name: "same_utc_day_different_local_dates", change: func(s []modelstore.Sample) {
			s[1].CollectedAt = day.Add(23 * time.Hour).In(time.FixedZone("east", 8*60*60)).Format(time.RFC3339Nano)
		}},
		{name: "separate_rounds_on_validated_days", change: func(s []modelstore.Sample) {
			for i := len(challenges); i < len(s); i++ {
				s[i].CollectedAt = otherDay.Add(10 * time.Hour).Format(time.RFC3339Nano)
			}
		}},
		{name: "first_timestamp_invalid", reason: "INVALID_SAMPLE_TIMESTAMP", change: func(s []modelstore.Sample) { s[0].CollectedAt = "not-a-date" }},
		{name: "later_timestamp_invalid", reason: "INVALID_SAMPLE_TIMESTAMP", change: func(s []modelstore.Sample) { s[1].CollectedAt = "not-a-date" }},
		{name: "later_timestamp_missing", reason: "INVALID_SAMPLE_TIMESTAMP", change: func(s []modelstore.Sample) { s[2].CollectedAt = "" }},
		{name: "later_timestamp_impossible", reason: "INVALID_SAMPLE_TIMESTAMP", change: func(s []modelstore.Sample) { s[1].CollectedAt = "2026-02-30T12:00:00Z" }},
		{name: "second_round_timestamp_invalid", reason: "INVALID_SAMPLE_TIMESTAMP", change: func(s []modelstore.Sample) { s[len(s)-1].CollectedAt = "not-a-date" }},
		{name: "cross_utc_day", reason: "CROSS_DAY_CHALLENGE_ROUND", change: func(s []modelstore.Sample) {
			s[1].CollectedAt = day.Add(24 * time.Hour).Format(time.RFC3339Nano)
		}},
		{name: "same_local_date_different_utc_day", reason: "CROSS_DAY_CHALLENGE_ROUND", change: func(s []modelstore.Sample) {
			s[1].CollectedAt = day.Add(-time.Hour).In(time.FixedZone("east", 8*60*60)).Format(time.RFC3339Nano)
		}},
		{name: "second_round_cross_utc_day", reason: "CROSS_DAY_CHALLENGE_ROUND", change: func(s []modelstore.Sample) {
			s[len(s)-1].CollectedAt = day.Add(24 * time.Hour).Format(time.RFC3339Nano)
		}},
		{name: "provider_changed", reason: "INCONSISTENT_CHALLENGE_ROUTE", change: func(s []modelstore.Sample) { s[1].RouteEvidence["provider_id"] = "held-fixture-channel" }},
		{name: "provider_model_changed", reason: "INCONSISTENT_CHALLENGE_ROUTE", change: func(s []modelstore.Sample) { s[2].RouteEvidence["provider_model"] = "other-model" }},
		{name: "second_round_provider_model_changed", reason: "INCONSISTENT_CHALLENGE_ROUTE", change: func(s []modelstore.Sample) { s[len(s)-1].RouteEvidence["provider_model"] = "other-model" }},
		{name: "provider_model_missing_in_whole_round", reason: "INCONSISTENT_CHALLENGE_ROUTE", change: func(s []modelstore.Sample) {
			for i := range s {
				delete(s[i].RouteEvidence, "provider_model")
			}
		}},
		{name: "provider_model_non_string", reason: "INCONSISTENT_CHALLENGE_ROUTE", change: func(s []modelstore.Sample) {
			for i := range s {
				s[i].RouteEvidence["provider_model"] = 17
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			samples := newSamples()
			if tc.change != nil {
				tc.change(samples)
			}
			result := modelstore.Result{IdentityStatus: "INCONCLUSIVE", Metrics: map[string]any{}}
			applyReferenceDecision(&result, modelstore.Target{ClaimedModel: "fixture-A"}, samples, entry)
			if tc.reason == "" {
				if result.IdentityStatus != "MATCHED" || result.PredictedModel != "fixture-A" || result.Metrics["identity_validated"] != true {
					t.Fatalf("trusted complete round rejected: %s / %s", result.IdentityStatus, result.RejectionReason)
				}
			} else if result.IdentityStatus != "INCONCLUSIVE" || result.RejectionReason != tc.reason || result.PredictedModel != "" || result.PredictedFamily != "" || result.Metrics["identity_validated"] != false {
				t.Fatalf("round escaped the required guard: %s / %s; wanted %s", result.IdentityStatus, result.RejectionReason, tc.reason)
			}
			if result.CalibratedProbability != nil {
				t.Fatal("round acquired an unfitted probability")
			}
		})
	}
}
