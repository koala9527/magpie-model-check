package attribution

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestExportedProtocolHashMatchesManifest(t *testing.T) {
	_, r, c := calibratedFixture(t, 8)
	h, err := ProtocolSHA256(c.Challenges, c.SystemPromptSHA256)
	if err != nil || h != r.ChallengeSetSHA256 {
		t.Fatalf("protocol hash = %s, %v; want %s", h, err, r.ChallengeSetSHA256)
	}
	c.Challenges[0], c.Challenges[1] = c.Challenges[1], c.Challenges[0]
	other, err := ProtocolSHA256(c.Challenges, c.SystemPromptSHA256)
	if err != nil || other != h {
		t.Fatal("prompt ordering changed semantic protocol hash")
	}
	c.Challenges[0].Prompt += "edited"
	if _, err := ProtocolSHA256(c.Challenges, c.SystemPromptSHA256); err == nil {
		t.Fatal("hashed a stale prompt digest")
	}
}

func TestReferenceBundlePreservesRawStringsAndSHA(t *testing.T) {
	bank, corpus := fixtureBytes(t, syntheticCorpus(3))
	raw, err := MarshalBundle(bank, corpus)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseBundle(raw)
	if err != nil || b.Format != "magpie-model-check.reference-bundle" || !bytes.Equal([]byte(b.BankJSON), bank) || !bytes.Equal([]byte(b.CorpusJSON), corpus) {
		t.Fatalf("bundle roundtrip changed bytes: %v", err)
	}
	e, r, err := LoadBundle(raw, Options{})
	if err != nil || r.BankSHA256 != digest(bank) || r.CorpusSHA256 != digest(corpus) {
		t.Fatalf("bundle binding: %+v %v", r, err)
	}
	proof, err := e.ProvenanceJSON()
	if err != nil || !json.Valid(proof) || !bytes.Contains(proof, []byte(r.CorpusSHA256)) || !bytes.Contains(proof, []byte(`"source_type":"synthetic"`)) {
		t.Fatalf("full provenance export missing binding: %v", err)
	}
	f, err := CredentialFindingsJSON(raw)
	if err != nil || len(f) != 0 {
		t.Fatalf("clean synthetic bundle scan: %+v %v", f, err)
	}
	legacy, _ := BuiltinLegacy()
	raw, err = MarshalBundle(legacy.RawBankJSON(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err = LoadBundle(raw, Options{})
	if err != nil || r.IdentityValidated {
		t.Fatalf("legacy bundle became trusted: %+v %v", r, err)
	}
}

func TestCredentialScanIncludesNestedRawJSONAndProvenance(t *testing.T) {
	// All credential-like values here are synthetic scan probes, not secrets.
	secret := "synthetic-credential-probe"
	inner := `{"label_evidence":{"headers":{"Authorization":"Bearer ` + secret + `"}},"request_parameters":{"api_key":"` + secret + `"},"raw_response":"password=` + secret + `"}`
	raw := encode(t, map[string]any{"format": "magpie-model-check.reference-bundle", "bank_json": `{"version":"synthetic"}`, "corpus_json": inner})
	f, err := CredentialFindingsJSON(raw)
	if err != nil || len(f) < 3 {
		t.Fatalf("failed full-envelope credential scan: %+v %v", f, err)
	}
	serialized := string(encode(t, f))
	if strings.Contains(serialized, secret) {
		t.Fatal("credential scan leaked values in findings")
	}
	if !strings.Contains(serialized, "corpus_json") || !strings.Contains(serialized, "label_evidence") {
		t.Fatal("provenance/string envelopes were not inspected")
	}
	for _, raw := range [][]byte{[]byte(`{"password":"one","password":"two"}`), []byte(`{"corpus_json":"{broken}"}`), []byte(strings.Repeat("[", 200) + "0" + strings.Repeat("]", 200))} {
		if _, err := CredentialFindingsJSON(raw); err == nil {
			t.Fatal("ambiguous, malformed or excessively deep scan input accepted")
		}
	}
}

func TestObservationSpecificParametersAreBound(t *testing.T) {
	c := syntheticCorpus(8)
	c.Challenges[1].Parameters = map[string]any{"temperature": .1}
	for i := range c.Records {
		if c.Records[i].ChallengeID == c.Challenges[1].ID {
			c.Records[i].RequestParameters = json.RawMessage(`{"temperature":0.1}`)
		}
	}
	bank, corpus := fixtureBytes(t, c)
	e, r, err := Load(bank, corpus, Options{Policy: mechanicsPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	q := fixtureRequest(c, r, "1 1 1 1", "1 1 1 1", "synthetic/A-v1")
	q.Observations[1].RequestParameters = json.RawMessage(`{"temperature":0.1}`)
	if d := e.decideMechanics(q); d.Status != Matched {
		t.Fatalf("valid per-challenge parameters: %+v", d)
	}
	q.Observations[1].RequestParameters = nil
	if d := e.decideMechanics(q); d.Status != Inconclusive || !hasReason(d.Reasons, "request_parameters_mismatch") {
		t.Fatalf("unbound parameters accepted: %+v", d)
	}
}
