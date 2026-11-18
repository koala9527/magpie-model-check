package attribution

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestExactParameterNumbersAndDuplicatesCannotMatchProtocol(t *testing.T) {
	c := syntheticCorpus(8)
	for i := range c.Challenges {
		c.Challenges[i].Parameters["seed"] = json.Number("9007199254740993")
	}
	for i := range c.Records {
		c.Records[i].RequestParameters = json.RawMessage(`{"temperature":0,"seed":9007199254740993}`)
	}
	bank, corpus := fixtureBytes(t, c)
	e, r, err := Load(bank, corpus, Options{Policy: mechanicsPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	q := fixtureRequest(c, r, "1 1 1 1", "1 1 1 1", "synthetic/A-v1")
	q.RequestParameters = json.RawMessage(`{"temperature":0,"seed":9007199254740993}`)
	if d := e.decideMechanics(q); d.Status != Matched {
		t.Fatalf("exact large integer parameter was rejected: %+v", d)
	}
	q.RequestParameters = json.RawMessage(`{"temperature":0,"seed":9007199254740992}`)
	if d := e.decideMechanics(q); d.Status != Inconclusive || !hasReason(d.Reasons, "request_parameters_mismatch") {
		t.Fatalf("float rounding accepted changed integer parameter: %+v", d)
	}
	q.RequestParameters = json.RawMessage(`{"temperature":1,"temperature":0,"seed":9007199254740993}`)
	if d := e.decideMechanics(q); d.Status != Inconclusive || !hasReason(d.Reasons, "request_parameters_mismatch") {
		t.Fatalf("ambiguous runtime JSON accepted: %+v", d)
	}
}

func TestUnknownCalibrationClassesCannotLeakIntoOtherHoldouts(t *testing.T) {
	for _, s := range []Split{ChannelHoldout, TimeHoldout} {
		t.Run(string(s), func(t *testing.T) {
			c := syntheticCorpus(2)
			for i := range c.Records {
				if c.Records[i].Split == s && c.Records[i].Family == "synthetic/unknown-family" {
					c.Records[i].Model = "synthetic/unknown-calibration"
				}
			}
			bank, corpus := fixtureBytes(t, c)
			if _, _, err := Load(bank, corpus, Options{}); err == nil {
				t.Fatal("unknown calibration identity leaked into final holdout")
			}
		})
	}
}

func TestObservedChannelAndDateCannotBeRecombinedAsValidatedScope(t *testing.T) {
	e, r, c := calibratedFixture(t, 8)
	q := fixtureRequest(c, r, "1 1 1 1", "1 1 1 1", "synthetic/A-v1")
	q.Channel = "synthetic-held-channel" // Known only on DIFFERENT holdout dates.
	if d := e.decideMechanics(q); d.Status != Inconclusive || !hasReason(d.Reasons, "channel_date_not_validated") {
		t.Fatalf("unobserved channel/date intersection accepted: %+v", d)
	}
}

func TestMissingProvenanceRecordsOrProofsRemainUnverified(t *testing.T) {
	// Adversarial real labels on synthetic fixtures exercise missing-evidence
	// guards. The mock auditor is NOT a real provenance authority.
	for _, emptyRecords := range []bool{true, false} {
		t.Run(fmt.Sprint(emptyRecords), func(t *testing.T) {
			c := syntheticCorpus(2)
			c.Kind = Real
			for i := range c.Records {
				c.Records[i].SourceType = "manual_import"
				c.Records[i].LabelEvidence = json.RawMessage(`null`)
				c.Records[i].LabelEvidenceSHA256 = digest([]byte(`null`))
			}
			if emptyRecords {
				c.Records = nil
			}
			bank, corpus := fixtureBytes(t, c)
			_, r, err := Load(bank, corpus, Options{VerifyProvenance: func(Binding) error { return nil }})
			if err != nil || r.ProvenanceValidated || r.IdentityValidated || !hasReason(r.Reasons, "provenance_") {
				t.Fatalf("missing provenance reported reviewed: validated=%v reasons=%v err=%v", r.ProvenanceValidated, r.Reasons, err)
			}
		})
	}
}
