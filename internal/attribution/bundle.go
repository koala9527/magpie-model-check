package attribution

import (
	"encoding/json"
	"fmt"

	"github.com/koala9527/magpie-model-check/internal/fingerprint"
)

const ReferenceBundleFormat = "magpie-model-check.reference-bundle"

// ReferenceBundle preserves both inner documents as original raw strings.
// Outer JSON formatting may change; neither bound inner document is rewritten.
type ReferenceBundle struct {
	Format     string `json:"format"`
	BankJSON   string `json:"bank_json"`
	CorpusJSON string `json:"corpus_json"`
}

// ProtocolSHA256 shares the exact protocol binding with the collecting service.
// It covers sorted, versioned full challenges and the system-prompt hash.
func ProtocolSHA256(challenges []fingerprint.Challenge, systemPromptSHA256 string) (string, error) {
	c := Corpus{SchemaVersion: SchemaVersion, AlgorithmVersion: fingerprint.AlgorithmVersion, Kind: Synthetic,
		SystemPromptSHA256: systemPromptSHA256, Challenges: challenges}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("protocol JSON: %w", err)
	}
	c, _, err = readCorpusHeader(raw)
	if err != nil {
		return "", err
	}
	return challengeHash(c), nil
}

func MarshalBundle(bankJSON, corpusJSON []byte) ([]byte, error) {
	var document any
	if err := strictJSON(bankJSON, &document); err != nil {
		return nil, err
	}
	if len(corpusJSON) > 0 {
		if err := strictJSON(corpusJSON, &document); err != nil {
			return nil, err
		}
	}
	return json.Marshal(ReferenceBundle{Format: ReferenceBundleFormat, BankJSON: string(bankJSON), CorpusJSON: string(corpusJSON)})
}

func ParseBundle(raw []byte) (ReferenceBundle, error) {
	var b ReferenceBundle
	if err := strictJSON(raw, &b); err != nil {
		return b, err
	}
	if b.Format != ReferenceBundleFormat || b.BankJSON == "" {
		return b, fmt.Errorf("unsupported or incomplete reference bundle")
	}
	var inner any
	if err := strictJSON([]byte(b.BankJSON), &inner); err != nil {
		return b, err
	}
	if b.CorpusJSON != "" {
		if err := strictJSON([]byte(b.CorpusJSON), &inner); err != nil {
			return b, err
		}
	}
	return b, nil
}

func LoadBundle(raw []byte, options Options) (*Engine, ValidationReport, error) {
	b, err := ParseBundle(raw)
	if err != nil {
		return nil, ValidationReport{}, err
	}
	return Load([]byte(b.BankJSON), []byte(b.CorpusJSON), options)
}

// ProvenanceJSON exposes complete bound records and challenge contracts for
// local credential scanning/auditing. It is not itself a provenance approval.
func (e *Engine) ProvenanceJSON() ([]byte, error) {
	bindings := []Binding{}
	if e != nil {
		for _, r := range e.corpus.Records {
			bindings = append(bindings, copyBinding(e.report.CorpusSHA256, r, e.challenges[r.ChallengeID]))
		}
	}
	return json.Marshal(bindings)
}
