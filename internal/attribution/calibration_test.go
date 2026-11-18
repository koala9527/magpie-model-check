package attribution

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// These policy numbers are permissive synthetic fixture targets, not proposed
// deployment requirements or real-model performance claims.
func mechanicsPolicy() *Policy {
	return &Policy{Confidence: .95, MaxFalseMatchRate: .4, MaxFalseReplacementRate: .4,
		MinUnknownRecall: .6, MaxKnownRejectionRate: .4, MinTop1Accuracy: .6, MinFamilyAccuracy: .6}
}

func calibratedFixture(t *testing.T, n int) (*Engine, ValidationReport, Corpus) {
	t.Helper()
	c := syntheticCorpus(n)
	bank, corpus := fixtureBytes(t, c)
	e, r, err := Load(bank, corpus, Options{Policy: mechanicsPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	return e, r, c
}

func TestDerivedCalibrationAndGroupedWilsonMetrics(t *testing.T) {
	_, r, _ := calibratedFixture(t, 8)
	if !r.MechanicsValidated || r.IdentityValidated || r.ProvenanceValidated || !hasReason(r.Reasons, "synthetic_mechanics_only") {
		t.Fatalf("synthetic mechanics validation: %+v", r)
	}
	for _, m := range fixtureModels() {
		th, ok := r.Thresholds[m.ID]
		if !ok || !finite(th.MaxDistance) || th.MaxDistance < 0 || th.MinMargin <= 0 || th.MinConsistency != 1 || th.CalibrationGroups != 8 || len(th.MaxChallengeDistance) != 2 {
			t.Fatalf("derived threshold %s = %+v", m.ID, th)
		}
	}
	known := r.BySplit[Test]
	if known.Groups != 16 || known.Top1Accuracy.Total != 16 || known.Top1Accuracy.Events != 16 || known.FalseMatchRate.Total != 16 || known.KnownRejectionRate.Events != 0 {
		t.Fatalf("counted responses/claims rather than independent groups: %+v", known)
	}
	if known.UnknownRecall.Value != nil || known.UnknownRecall.Interval != nil {
		t.Fatal("invented recall for absent population")
	}
	u := r.BySplit[UnknownTest]
	if u.Groups != 8 || u.UnknownRecall.Events != 8 || u.UnknownRecall.Total != 8 || u.UnknownRecall.Interval == nil || u.UnknownRecall.Interval.Lower < .6 {
		t.Fatalf("unknown metrics: %+v", u)
	}
	if known.BrierScore != nil || known.CalibrationError != nil {
		t.Fatal("invented probability calibration metrics")
	}
	if len(r.ByChannel) != 2 || len(r.ByDate) == 0 || r.ByModel["synthetic/A-v1"].Groups != 24 {
		t.Fatalf("stratification missing: %+v", r)
	}
}

func TestThresholdsNeverUseFinalHoldoutLabelsOrValues(t *testing.T) {
	_, baseline, c := calibratedFixture(t, 8)
	want := encode(t, baseline.Thresholds)
	for i := range c.Records {
		r := &c.Records[i]
		if r.Split == Test && r.Model == "synthetic/A-v1" || r.Split == UnknownTest {
			r.RawResponse = "355 355 355 355"
			r.ResponseSHA256 = digest([]byte(r.RawResponse))
		}
	}
	bank, corpus := fixtureBytes(t, c)
	_, r, err := Load(bank, corpus, Options{Policy: mechanicsPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, encode(t, r.Thresholds)) {
		t.Fatal("test/unknown holdout tuned thresholds")
	}
	if r.MechanicsValidated || r.IdentityValidated || r.BySplit[UnknownTest].UnknownRecall.Events != 0 || r.BySplit[Test].FalseReplacementRate.Events != 8 {
		t.Fatalf("missed final holdout failures: %+v", r)
	}
	if !hasReason(r.Reasons, "validation_target_not_met:") {
		t.Fatal("missing explicit failed metric reason")
	}
}

func TestSampleAdequacyIsDrivenByCIAndExplicitPolicy(t *testing.T) {
	_, r, _ := calibratedFixture(t, 1)
	if r.MechanicsValidated || r.IdentityValidated || !hasReason(r.Reasons, "validation_target_not_met:") {
		t.Fatalf("declared one synthetic group adequate: %+v", r)
	}
	_, large, _ := calibratedFixture(t, 8)
	if !large.MechanicsValidated {
		t.Fatalf("permissive synthetic policy not met: %+v", large)
	}
	bank, corpus := fixtureBytes(t, syntheticCorpus(8))
	p := mechanicsPolicy()
	p.MaxFalseMatchRate = 0
	_, strict, err := Load(bank, corpus, Options{Policy: p})
	if err != nil || strict.MechanicsValidated || !hasReason(strict.Reasons, "validation_target_not_met:") {
		t.Fatalf("zero target claimed from finite trials: %+v %v", strict, err)
	}
	_, missing, err := Load(bank, corpus, Options{})
	if err != nil || missing.MechanicsValidated || !hasReason(missing.Reasons, "validation_policy_missing") {
		t.Fatalf("implicit policy: %+v %v", missing, err)
	}
}

func TestInvalidPoliciesRejected(t *testing.T) {
	bank, corpus := fixtureBytes(t, syntheticCorpus(2))
	for _, change := range []func(*Policy){
		func(p *Policy) { p.Confidence = 1 }, func(p *Policy) { p.MaxFalseMatchRate = -1 },
		func(p *Policy) { p.MinUnknownRecall = math.NaN() }, func(p *Policy) { p.MaxKnownRejectionRate = math.Inf(1) },
	} {
		p := mechanicsPolicy()
		change(p)
		if _, _, err := Load(bank, corpus, Options{Policy: p}); err == nil {
			t.Fatal("accepted invalid policy")
		}
	}
}

func TestSyntheticAndImportedValidatedCannotEnableRealVerdicts(t *testing.T) {
	e, r, c := calibratedFixture(t, 8)
	req := fixtureRequest(c, r, "1 1 1 1", "1 1 1 1", "synthetic/A-v1")
	d := e.Decide(req)
	if d.Status != Inconclusive || d.IdentityValidated || d.CalibratedProbability != nil || !hasReason(d.Reasons, "synthetic_mechanics_only") {
		t.Fatalf("synthetic promoted to identity: %+v", d)
	}
	bank, corpus := fixtureBytes(t, c)
	var b Bank
	_ = json.Unmarshal(bank, &b)
	b.Validated = true
	b.Thresholds = map[string]Threshold{"synthetic/A-v1": {MaxDistance: 1, MinMargin: 0, MinConsistency: 0}}
	b.ValidationMetrics = json.RawMessage(`{"identity_validated":true}`)
	e, r, err := Load(encode(t, b), corpus, Options{Policy: mechanicsPolicy()})
	if err != nil || r.IdentityValidated || e.Decide(req).Status != Inconclusive {
		t.Fatalf("import bypass: %+v %v", r, err)
	}
	// Adversarial real label on synthetic fixtures, with no independent auditor.
	c.Kind = Real
	for i := range c.Records {
		c.Records[i].SourceType = "manual_import"
	}
	bank, corpus = fixtureBytes(t, c)
	e, r, err = Load(bank, corpus, Options{Policy: mechanicsPolicy()})
	if err != nil || r.IdentityValidated || e.Decide(req).Status != Inconclusive || !hasReason(r.Reasons, "provenance_verifier_missing") {
		t.Fatalf("real provenance bypass: %+v %v", r, err)
	}
}

func fixtureRequest(c Corpus, r ValidationReport, a, b, claimed string) Request {
	day := ""
	for _, rec := range c.Records {
		if rec.Split == Test {
			day = rec.CollectedAt
			break
		}
	}
	return Request{ClaimedModel: claimed, BankVersion: r.BankVersion, AlgorithmVersion: r.AlgorithmVersion,
		ChallengeSetSHA256: r.ChallengeSetSHA256, Channel: "synthetic-base-channel", CollectedAt: day,
		SystemPromptSHA256: c.SystemPromptSHA256, RequestParameters: json.RawMessage(`{"temperature":0}`), RouteVerified: true,
		Observations: []Observation{{ChallengeID: c.Challenges[0].ID, RawResponse: a, FinishReason: "stop"}, {ChallengeID: c.Challenges[1].ID, RawResponse: b, FinishReason: "stop"}}}
}

func TestMechanicalOpenSetAndConsistencyDecisions(t *testing.T) {
	e, r, c := calibratedFixture(t, 8)
	for _, tc := range []struct {
		name, a, b, claimed string
		status              Status
		predicted           string
	}{
		{"matched", "1 1 1 1", "1 1 1 1", "synthetic/A-v1", Matched, "synthetic/A-v1"},
		{"mismatched", "1 1 1 1", "1 1 1 1", "synthetic/B-v1", Mismatched, "synthetic/A-v1"},
		{"unknown", "178 178 178 178", "178 178 178 178", "synthetic/A-v1", Unknown, ""},
		{"inconsistent", "1 1 1 1", "355 355 355 355", "synthetic/A-v1", Inconclusive, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := e.decideMechanics(fixtureRequest(c, r, tc.a, tc.b, tc.claimed))
			if d.Status != tc.status || d.PredictedModel != tc.predicted || d.CalibratedProbability != nil || d.IdentityValidated {
				t.Fatalf("SYNTHETIC mechanical decision: %+v", d)
			}
		})
	}
}

func TestAmbiguousAdjacentVersionsAreNotForcedMatched(t *testing.T) {
	c := syntheticCorpus(8)
	for i := range c.Records {
		r := &c.Records[i]
		if r.Model == "synthetic/B-v1" {
			r.Model = "synthetic/A-v2"
			r.Family = "synthetic/family-A"
			r.RawResponse = "1 1 1 1"
			r.ResponseSHA256 = digest([]byte(r.RawResponse))
		}
	}
	corpus := encode(t, c)
	bank, err := MarshalBank(corpus, "synthetic-neighbors", []Model{{"synthetic/A-v1", "synthetic/family-A"}, {"synthetic/A-v2", "synthetic/family-A"}})
	if err != nil {
		t.Fatal(err)
	}
	e, r, err := Load(bank, corpus, Options{Policy: mechanicsPolicy()})
	if err != nil || r.MechanicsValidated || !hasReason(r.Reasons, "calibration_not_separable:") {
		t.Fatalf("neighbor validation: %+v %v", r, err)
	}
	d := e.decideMechanics(fixtureRequest(c, r, "1 1 1 1", "1 1 1 1", "synthetic/A-v1"))
	if d.Status != Inconclusive || d.PredictedModel != "" {
		t.Fatalf("forced adjacent version: %+v", d)
	}
}

func TestRequestGuardsProduceExplicitInconclusiveReasons(t *testing.T) {
	e, r, c := calibratedFixture(t, 8)
	for _, tc := range []struct {
		name, reason string
		change       func(*Request)
	}{
		{"route", "route_unverified", func(q *Request) { q.RouteVerified = false }},
		{"bank", "bank_version_mismatch", func(q *Request) { q.BankVersion = "stale" }},
		{"algorithm", "algorithm_version_mismatch", func(q *Request) { q.AlgorithmVersion = "other" }},
		{"protocol", "challenge_protocol_mismatch", func(q *Request) { q.ChallengeSetSHA256 = "other" }},
		{"system", "system_prompt_mismatch", func(q *Request) { q.SystemPromptSHA256 = digest([]byte("other")) }},
		{"parameters", "request_parameters_mismatch", func(q *Request) { q.RequestParameters = json.RawMessage(`{"temperature":1}`) }},
		{"claim", "claimed_model_not_in_bank", func(q *Request) { q.ClaimedModel = "not-known" }},
		{"missing", "incomplete_challenge_set", func(q *Request) { q.Observations = q.Observations[:1] }},
		{"duplicate", "duplicate_challenge", func(q *Request) { q.Observations[1].ChallengeID = q.Observations[0].ChallengeID }},
		{"invalid", "invalid_sample:", func(q *Request) { q.Observations[0].RawResponse = "1 -1 1 1" }},
		{"truncated", "invalid_sample:", func(q *Request) { q.Observations[0].FinishReason = "length" }},
		{"channel", "channel_not_validated", func(q *Request) { q.Channel = "unseen-channel" }},
		{"future", "date_not_validated", func(q *Request) { q.CollectedAt = "2100-01-01T00:00:00Z" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := fixtureRequest(c, r, "1 1 1 1", "1 1 1 1", "synthetic/A-v1")
			tc.change(&q)
			d := e.Decide(q)
			if d.Status != Inconclusive || !hasReason(d.Reasons, tc.reason) {
				t.Fatalf("%s: %+v", tc.name, d)
			}
		})
	}
}

func TestProbabilityFieldsRemainNullInSerializedReports(t *testing.T) {
	e, r, c := calibratedFixture(t, 8)
	data := string(encode(t, e.Decide(fixtureRequest(c, r, "1 1 1 1", "1 1 1 1", "synthetic/A-v1"))))
	if !strings.Contains(data, `"calibrated_probability":null`) {
		t.Fatal(data)
	}
	data = string(encode(t, r))
	if !strings.Contains(data, `"brier_score":null`) || !strings.Contains(data, `"calibration_error":null`) {
		t.Fatal("reported unfitted calibration metrics")
	}
}
