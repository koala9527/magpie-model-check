package modelstore

import (
	"testing"
)

func TestRestoreNeverImportsRouteOrIdentityAuthority(t *testing.T) {
	source := openTestStore(t)
	target, batch, sample, result, _, bank := seedRecords(t, source)
	target.LastStatus, target.AutoCheck = "MATCHED", true
	if _, err := source.PutTarget(target); err != nil {
		t.Fatal(err)
	}
	batch.Status, batch.RouteBindingStatus = "MATCHED", "VERIFIED"
	if err := source.PutBatch(batch); err != nil {
		t.Fatal(err)
	}
	sample.RouteEvidence = map[string]any{"authenticated": true, "protocol_verified": true, "provider_id": "never-observed"}
	if err := source.PutSample(sample); err != nil {
		t.Fatal(err)
	}
	probability := 0.99
	result.IdentityStatus, result.CalibratedProbability = "MATCHED", &probability
	result.Metrics = map[string]any{"identity_validated": true, "bank_trusted": true, "protocol_verified": true}
	if err := source.PutResult(result); err != nil {
		t.Fatal(err)
	}
	bank.Trusted = true
	bank.ValidationMetrics = map[string]any{"identity_validated": true, "independent_validation": true}
	if err := source.PutBank(bank); err != nil {
		t.Fatal(err)
	}
	dest := openTestStore(t)
	if err := dest.Restore(exportTest(t, source)); err != nil {
		t.Fatal(err)
	}
	gotTarget, _ := dest.Target(target.ID)
	gotBatch, _ := dest.Batch(batch.ID)
	gotSamples, _ := dest.Samples(batch.ID)
	gotResult, _ := dest.Result(batch.ID)
	gotBank, _ := dest.Bank(bank.ID)
	if gotTarget.LastStatus != "PENDING" || gotTarget.AutoCheck {
		t.Fatal("imported target retained identity/scheduling authority", gotTarget)
	}
	if gotBatch.Status != "INCONCLUSIVE" || gotBatch.RouteBindingStatus == "VERIFIED" {
		t.Fatal("imported batch asserted verified identity/route", gotBatch)
	}
	if gotSamples[0].RouteEvidence["authenticated"] == true || gotSamples[0].RouteEvidence["protocol_verified"] == true {
		t.Fatal("unsigned backup granted route authority")
	}
	if gotResult.IdentityStatus != "INCONCLUSIVE" || gotResult.PredictedModel != "" || gotResult.CalibratedProbability != nil || gotResult.Metrics["identity_validated"] == true {
		t.Fatal("backup granted identity authority", gotResult)
	}
	if gotBank.Trusted || gotBank.ValidationMetrics["identity_validated"] == true {
		t.Fatal("backup granted reference authority")
	}
	if gotSamples[0].RawResponse != sample.RawResponse || string(gotBank.Data) != string(bank.Data) {
		t.Fatal("restore lost raw evidence")
	}
}
