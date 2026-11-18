// Package fingerprint reproduces the pinned ModelTrace numeric scorer.
// Rankings compare behavior against a legacy closed-set bank, not identity.
package fingerprint

// AlgorithmVersion identifies the mathematics and pinned legacy reference bank.
const AlgorithmVersion = "modeltrace-6cd4a8ff-v1"

// ParserVersion identifies the strict parser, separately from the legacy scorer.
const ParserVersion = "strict-integers-v1"

// Challenge describes the exact prompt and parser contract of one sample.
// SHA256 is the lowercase SHA-256 digest of Prompt's UTF-8 bytes.
// Parameters contains explicit request overrides; an empty map preserves the
// legacy request defaults, rather than inventing sampling settings.
type Challenge struct {
	ID            string         `json:"id"`
	Version       int            `json:"version"`
	Prompt        string         `json:"prompt"`
	SHA256        string         `json:"sha256"`
	Min           int            `json:"min"`
	Max           int            `json:"max"`
	Count         int            `json:"count"`
	ParserVersion string         `json:"parser_version"`
	Parameters    map[string]any `json:"parameters"`
}

// Candidate is a legacy-bank comparison, not a probability or verdict.
// Score is ModelTrace's robust blended score (larger is closer).
// Distance is the normalized Jensen-Shannon distance (smaller is closer).
type Candidate struct {
	Model    string  `json:"model"`
	Family   string  `json:"family"`
	Score    float64 `json:"score"`
	Distance float64 `json:"distance"`
}
