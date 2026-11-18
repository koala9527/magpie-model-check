package attribution

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCredentialScanErrorsNeverEchoCredentialKeys(t *testing.T) {
	// Synthetic privacy probe, never a real credential.
	probe := "synthetic-credential-probe"
	raw := []byte(`{"Bearer ` + probe + `":0,"Bearer ` + probe + `":1}`)
	_, err := CredentialFindingsJSON(raw)
	if err == nil || strings.Contains(err.Error(), probe) || strings.Contains(err.Error(), "Bearer") {
		t.Fatalf("credential scan error leaked probe: %v", err)
	}
}

func TestStructFieldAliasesAreRejectedButFreeMapsKeepCase(t *testing.T) {
	bank, corpus := fixtureBytes(t, syntheticCorpus(2))
	raw := bytes.Replace(bank, []byte(`"version":"synthetic-bank-v1"`), []byte(`"version":"synthetic-bank-v1","VERSION":"alternate"`), 1)
	if _, _, err := Load(raw, corpus, Options{}); err == nil {
		t.Fatal("case alias overrode a tagged struct field")
	}
	var proof map[string]any
	if err := strictJSON([]byte(`{"case":"one","CASE":"two"}`), &proof); err != nil || len(proof) != 2 {
		t.Fatalf("free proof map case semantics lost: %+v %v", proof, err)
	}
	raw = bytes.Replace(corpus, []byte(`"challenge_id":`), []byte(`"CHALLENGE_ID":"synthetic-challenge-2","challenge_id":`), 1)
	// Rebind bytes with the old valid manifest rather than letting MarshalBank
	// reject them first; Load must independently reject nested struct aliases.
	var b Bank
	_ = json.Unmarshal(bank, &b)
	b.CorpusSHA256 = digest(raw)
	if _, _, err := Load(encode(t, b), raw, Options{}); err == nil {
		t.Fatal("nested record alias accepted")
	}
}

func TestInvalidUTF8IsRejectedBeforeHashOrBundleSerialization(t *testing.T) {
	_, corpus := fixtureBytes(t, syntheticCorpus(2))
	corrupt := bytes.ReplaceAll(corpus, []byte("synthetic-base-channel"), append([]byte{0xff}, []byte("ynthetic-base-channel")...))
	bank, err := MarshalBank(corrupt, "synthetic-invalid-utf8", fixtureModels())
	if err == nil {
		t.Error("MarshalBank accepted invalid UTF-8 corpus")
	}
	if len(bank) > 0 {
		if _, _, err := Load(bank, corrupt, Options{}); err == nil {
			t.Error("Load accepted invalid UTF-8 corpus")
		}
	}
	validBank, _ := fixtureBytes(t, syntheticCorpus(2))
	if _, err := MarshalBundle(validBank, corrupt); err == nil {
		t.Fatal("bundle serialization replaced raw bytes instead of rejecting invalid UTF-8")
	}
}
