package attribution

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/koala9527/magpie-model-check/internal/fingerprint"
)

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// All records below are synthetic mechanics fixtures, NOT real model evidence.
// Distinct group/session/batch/channel/time labels exercise split validation;
// they do not assert statistically independent physical model collections.
func syntheticCorpus(n int) Corpus {
	c := Corpus{SchemaVersion: SchemaVersion, AlgorithmVersion: fingerprint.AlgorithmVersion, Kind: Synthetic, SystemPromptSHA256: digest(nil)}
	for i := 1; i <= 2; i++ {
		prompt := fmt.Sprintf("合成机制测试 %d：返回四个 1 到 355 的整数。", i)
		c.Challenges = append(c.Challenges, fingerprint.Challenge{
			ID: fmt.Sprintf("synthetic-challenge-%d", i), Version: 1,
			Prompt: prompt, SHA256: digest([]byte(prompt)), Min: 1, Max: 355, Count: 4,
			ParserVersion: fingerprint.ParserVersion, Parameters: map[string]any{"temperature": 0},
		})
	}
	day := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	group := 0
	add := func(split Split, model, family, raw string, count int) {
		for j := 0; j < count; j++ {
			group++
			id := fmt.Sprintf("synthetic-group-%03d", group)
			channel := "synthetic-base-channel"
			if split == ChannelHoldout {
				channel = "synthetic-held-channel"
			}
			for k, ch := range c.Challenges {
				proof := json.RawMessage(fmt.Sprintf(`{"kind":"synthetic","group":"%s","challenge":%d}`, id, k))
				c.Records = append(c.Records, Record{
					ID: fmt.Sprintf("%s-record-%d", id, k), GroupID: id, BatchID: id + "-batch", SessionID: id + "-session",
					Split: split, Model: model, Family: family, Channel: channel,
					CollectedAt: day.AddDate(0, 0, group).Format(time.RFC3339), SourceType: "synthetic",
					ChallengeID: ch.ID, RawResponse: raw, ResponseSHA256: digest([]byte(raw)), FinishReason: "stop",
					RequestID: fmt.Sprintf("%s-request-%d", id, k), RequestParameters: json.RawMessage(`{"temperature":0}`),
					SystemPromptSHA256: c.SystemPromptSHA256,
					LabelEvidence:      proof, LabelEvidenceSHA256: digest(proof), RouteEvidence: proof, RouteEvidenceSHA256: digest(proof),
				})
			}
		}
	}
	for _, split := range []Split{Reference, Calibration, Test, UnknownTest, ChannelHoldout, TimeHoldout} {
		if split == UnknownTest {
			add(split, "synthetic/unknown-test", "synthetic/unknown-family", "178 178 178 178", n)
			continue
		}
		add(split, "synthetic/A-v1", "synthetic/family-A", "1 1 1 1", n)
		add(split, "synthetic/B-v1", "synthetic/family-B", "355 355 355 355", n)
		if split == Calibration {
			add(split, "synthetic/unknown-calibration", "synthetic/unknown-family", "177 177 177 177", n)
		}
		if split == ChannelHoldout || split == TimeHoldout {
			add(split, "synthetic/unknown-"+string(split), "synthetic/unknown-family", "178 178 178 178", n)
		}
	}
	return c
}

func fixtureModels() []Model {
	return []Model{{ID: "synthetic/A-v1", Family: "synthetic/family-A"}, {ID: "synthetic/B-v1", Family: "synthetic/family-B"}}
}

func fixtureBytes(t *testing.T, c Corpus) ([]byte, []byte) {
	t.Helper()
	corpus := encode(t, c)
	bank, err := MarshalBank(corpus, "synthetic-bank-v1", fixtureModels())
	if err != nil {
		t.Fatal(err)
	}
	return bank, corpus
}

func hasReason(reasons []string, prefix string) bool {
	for _, r := range reasons {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

func TestRawCorpusBindingAndIndependentCopies(t *testing.T) {
	bank, corpus := fixtureBytes(t, syntheticCorpus(3))
	e, r, err := Load(bank, corpus, Options{})
	if err != nil || e == nil || !r.IntegrityValidated || r.IdentityValidated {
		t.Fatalf("Load mechanics: %+v, %v", r, err)
	}
	if r.BankSHA256 != digest(bank) || r.CorpusSHA256 != digest(corpus) || r.ChallengeSetSHA256 == "" {
		t.Fatalf("raw bindings missing: %+v", r)
	}
	if !bytes.Equal(e.RawBankJSON(), bank) || !bytes.Equal(e.RawCorpusJSON(), corpus) {
		t.Fatal("raw exports changed bytes")
	}
	e.RawBankJSON()[0] = '!'
	e.RawCorpusJSON()[0] = '!'
	bank[0] = '!'
	corpus[0] = '!'
	r.IdentityValidated = true
	r.Reasons = nil
	if e.Report().IdentityValidated || e.RawBankJSON()[0] == '!' || e.RawCorpusJSON()[0] == '!' {
		t.Fatal("caller mutated engine")
	}
}

func TestRawHashAndImportedDerivedFields(t *testing.T) {
	bank, corpus := fixtureBytes(t, syntheticCorpus(3))
	if _, _, err := Load(bank, append(corpus, '\n'), Options{}); err == nil {
		t.Fatal("accepted unbound raw corpus whitespace")
	}
	var b Bank
	if err := json.Unmarshal(bank, &b); err != nil {
		t.Fatal(err)
	}
	b.Validated = true
	b.Thresholds = map[string]Threshold{"synthetic/A-v1": {MaxDistance: 999, MinMargin: -999, MinConsistency: -999}}
	b.ValidationMetrics = json.RawMessage(`{"validated":true,"accuracy":1,"calibrated_probability":1}`)
	e, r, err := Load(encode(t, b), corpus, Options{})
	if err != nil || r.IdentityValidated || e.Decide(Request{}).Status != Inconclusive {
		t.Fatalf("trusted import: %+v %v", r, err)
	}
	if th, ok := r.Thresholds["synthetic/A-v1"]; ok && th.MaxDistance == 999 {
		t.Fatal("used imported thresholds")
	}
}

func TestCorpusRejectsMalformedContractsAndRawValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Corpus)
	}{
		{"algorithm", func(c *Corpus) { c.AlgorithmVersion = "other" }},
		{"schema", func(c *Corpus) { c.SchemaVersion = 2 }},
		{"kind", func(c *Corpus) { c.Kind = "unrecognized" }},
		{"prompt_hash", func(c *Corpus) { c.Challenges[0].Prompt += "edited" }},
		{"parser", func(c *Corpus) { c.Challenges[0].ParserVersion = "legacy" }},
		{"duplicate_challenge", func(c *Corpus) { c.Challenges[1] = c.Challenges[0] }},
		{"challenge_count", func(c *Corpus) { c.Challenges[0].Count = 0 }},
		{"raw_hash", func(c *Corpus) { c.Records[0].RawResponse = "2 2 2 2" }},
		{"negative", func(c *Corpus) {
			r := &c.Records[0]
			r.RawResponse = "1 -2 1 1"
			r.ResponseSHA256 = digest([]byte(r.RawResponse))
		}},
		{"truncated", func(c *Corpus) { c.Records[0].FinishReason = "length" }},
		{"text", func(c *Corpus) {
			r := &c.Records[0]
			r.RawResponse = "1 1 1 1（完成）"
			r.ResponseSHA256 = digest([]byte(r.RawResponse))
		}},
		{"parameters", func(c *Corpus) { c.Records[0].RequestParameters = json.RawMessage(`{"temperature":1}`) }},
		{"system_prompt", func(c *Corpus) { c.Records[0].SystemPromptSHA256 = digest([]byte("changed")) }},
		{"label_hash", func(c *Corpus) { c.Records[0].LabelEvidence = json.RawMessage(`{"validated":true}`) }},
		{"route_hash", func(c *Corpus) { c.Records[0].RouteEvidence = json.RawMessage(`{"validated":true}`) }},
		{"timestamp", func(c *Corpus) { c.Records[0].CollectedAt = "not-a-date" }},
		{"missing_request", func(c *Corpus) { c.Records[0].RequestID = "" }},
		{"family", func(c *Corpus) { c.Records[0].Family = "different-family" }},
		{"source", func(c *Corpus) { c.Records[0].SourceType = "official_api_reference" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := syntheticCorpus(2)
			tc.change(&c)
			corpus := encode(t, c)
			bank, err := MarshalBank(corpus, "synthetic-bank-v1", fixtureModels())
			if err == nil {
				_, _, err = Load(bank, corpus, Options{})
			}
			if err == nil {
				t.Fatal("accepted corrupt contract or raw record")
			}
		})
	}
}

func TestCorpusRejectsGroupAndHoldoutLeakage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Corpus)
	}{
		{"record_id", func(c *Corpus) { c.Records[2].ID = c.Records[0].ID }},
		{"group_across_split", func(c *Corpus) { c.Records[4].GroupID = c.Records[0].GroupID }},
		{"batch", func(c *Corpus) { c.Records[2].BatchID = c.Records[0].BatchID }},
		{"session", func(c *Corpus) { c.Records[2].SessionID = c.Records[0].SessionID }},
		{"request", func(c *Corpus) { c.Records[2].RequestID = c.Records[0].RequestID }},
		{"channel", func(c *Corpus) {
			for i := range c.Records {
				if c.Records[i].Split == ChannelHoldout {
					c.Records[i].Channel = "synthetic-base-channel"
				}
			}
		}},
		{"time", func(c *Corpus) {
			for i := range c.Records {
				if c.Records[i].Split == TimeHoldout {
					c.Records[i].CollectedAt = c.Records[0].CollectedAt
				}
			}
		}},
		{"time_not_forward", func(c *Corpus) {
			for i := range c.Records {
				if c.Records[i].Split == TimeHoldout {
					c.Records[i].CollectedAt = "2024-01-01T00:00:00Z"
				}
			}
		}},
		{"unknown_known_label", func(c *Corpus) {
			for i := range c.Records {
				if c.Records[i].Split == UnknownTest {
					c.Records[i].Model = "synthetic/A-v1"
					c.Records[i].Family = "synthetic/family-A"
				}
			}
		}},
		{"unknown_calibration_identity", func(c *Corpus) {
			for i := range c.Records {
				if c.Records[i].Split == UnknownTest {
					c.Records[i].Model = "synthetic/unknown-calibration"
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := syntheticCorpus(2)
			tc.change(&c)
			bank, corpus := fixtureBytes(t, c)
			if _, _, err := Load(bank, corpus, Options{}); err == nil {
				t.Fatal("accepted leakage")
			}
		})
	}
}

func TestMissingPartitionsAndRealProvenanceAreInconclusive(t *testing.T) {
	c := syntheticCorpus(2)
	kept := c.Records[:0]
	for _, r := range c.Records {
		if r.Split != UnknownTest {
			kept = append(kept, r)
		}
	}
	c.Records = kept
	bank, corpus := fixtureBytes(t, c)
	e, r, err := Load(bank, corpus, Options{})
	if err != nil || r.IdentityValidated || !hasReason(r.Reasons, "missing_partition:unknown_test") || e.Decide(Request{}).Status != Inconclusive {
		t.Fatalf("missing partition: %+v %v", r, err)
	}
	// Adversarial relabeling of SYNTHETIC bytes as real cannot supply provenance.
	c = syntheticCorpus(2)
	c.Kind = Real
	for i := range c.Records {
		c.Records[i].SourceType = "manual_import"
	}
	bank, corpus = fixtureBytes(t, c)
	_, r, err = Load(bank, corpus, Options{})
	if err != nil || r.ProvenanceValidated || r.IdentityValidated || !hasReason(r.Reasons, "provenance_verifier_missing") {
		t.Fatalf("missing real auditor: %+v %v", r, err)
	}
	_, r, err = Load(bank, corpus, Options{VerifyProvenance: func(b Binding) error {
		if b.CorpusSHA256 != digest(corpus) || b.Record.ResponseSHA256 != digest([]byte(b.Record.RawResponse)) {
			t.Fatal("auditor got unbound record")
		}
		return fmt.Errorf("synthetic evidence is not a real-model collection")
	}})
	if err != nil || r.ProvenanceValidated || r.IdentityValidated || !hasReason(r.Reasons, "provenance_rejected:") {
		t.Fatalf("rejected auditor: %+v %v", r, err)
	}
}

func TestStrictJSONRejectsDuplicateFieldsAndTrailingData(t *testing.T) {
	bank, corpus := fixtureBytes(t, syntheticCorpus(2))
	for _, raw := range [][]byte{
		bytes.Replace(bank, []byte(`"version":`), []byte(`"validated":false,"validated":true,"version":`), 1),
		append(append([]byte(nil), bank...), []byte(` {}`)...),
		bytes.Replace(bank, []byte(`"family":`), []byte(`"id":"duplicate","id":"duplicate-again","family":`), 1),
		bytes.Replace(bank, []byte(`"format":`), []byte(`"hidden_threshold":1,"format":`), 1),
	} {
		if _, _, err := Load(raw, corpus, Options{}); err == nil {
			t.Fatalf("accepted ambiguous bank JSON %s", raw[:min(150, len(raw))])
		}
	}
}

func TestProvenanceCallbackCannotMutateOwnedProtocol(t *testing.T) {
	// These SYNTHETIC records are adversarially labeled real only to exercise the
	// auditor boundary; no policy is supplied and no identity claim is expected.
	c := syntheticCorpus(2)
	c.Kind = Real
	for i := range c.Records {
		c.Records[i].SourceType = "manual_import"
	}
	bank, corpus := fixtureBytes(t, c)
	e, r, err := Load(bank, corpus, Options{VerifyProvenance: func(b Binding) error {
		if !sameJSON(encode(t, b.Challenge.Parameters), []byte(`{"temperature":0}`)) {
			t.Errorf("auditor received mutated protocol: %+v", b.Challenge.Parameters)
		}
		b.Challenge.Parameters["temperature"] = 99
		b.Record.RequestParameters[0] = '!'
		b.Record.RouteEvidence[0] = '!'
		return fmt.Errorf("synthetic mock audit, not real provenance")
	}})
	if err != nil || r.IdentityValidated || !sameJSON(encode(t, e.challenges[c.Challenges[0].ID].Parameters), []byte(`{"temperature":0}`)) {
		t.Fatalf("callback changed owned state: %+v %v", r, err)
	}
}
