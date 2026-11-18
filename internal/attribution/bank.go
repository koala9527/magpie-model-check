package attribution

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/koala9527/magpie-model-check/internal/fingerprint"
)

// MarshalBank makes a manifest bound to raw corpus bytes. It cannot attest to
// origin or statistics, and intentionally emits validated=false.
func MarshalBank(corpusJSON []byte, version string, models []Model) ([]byte, error) {
	c, _, err := readCorpusHeader(corpusJSON)
	if err != nil {
		return nil, err
	}
	if !identifier(version) {
		return nil, fmt.Errorf("bank version missing or malformed")
	}
	if err := validateModels(models); err != nil {
		return nil, err
	}
	b := Bank{Format: BankFormat, SchemaVersion: SchemaVersion, Version: version,
		AlgorithmVersion: fingerprint.AlgorithmVersion, MethodVersion: MethodVersion,
		CorpusSHA256: sha(corpusJSON), ChallengeSetSHA256: challengeHash(c),
		Kind: c.Kind, Models: append([]Model(nil), models...), SourceType: "raw_corpus"}
	return json.Marshal(b)
}

// Load recomputes from bound raw records; imported booleans, thresholds and
// validation metrics do not influence the owned Engine.
func Load(bankJSON, corpusJSON []byte, options Options) (*Engine, ValidationReport, error) {
	if len(corpusJSON) == 0 && bytes.Equal(bankJSON, fingerprint.LegacyBankJSON()) {
		e, r := BuiltinLegacy()
		return e, r, nil
	}
	var b Bank
	if err := strictJSON(bankJSON, &b); err != nil {
		return nil, ValidationReport{}, err
	}
	if b.Format != BankFormat || b.SchemaVersion != SchemaVersion || !identifier(b.Version) || b.AlgorithmVersion != fingerprint.AlgorithmVersion || b.MethodVersion != MethodVersion || b.SourceType != "raw_corpus" {
		return nil, ValidationReport{}, fmt.Errorf("unsupported bank format, version, method or source")
	}
	if err := validateModels(b.Models); err != nil {
		return nil, ValidationReport{}, err
	}
	if !validSHA(b.CorpusSHA256) || b.CorpusSHA256 != sha(corpusJSON) {
		return nil, ValidationReport{}, fmt.Errorf("raw corpus SHA-256 mismatch")
	}
	c, challenges, err := readCorpusHeader(corpusJSON)
	if err != nil {
		return nil, ValidationReport{}, err
	}
	if b.Kind != c.Kind || !validSHA(b.ChallengeSetSHA256) || b.ChallengeSetSHA256 != challengeHash(c) {
		return nil, ValidationReport{}, fmt.Errorf("dataset kind or challenge protocol binding mismatch")
	}
	if options.Policy != nil {
		if err := validatePolicy(*options.Policy); err != nil {
			return nil, ValidationReport{}, err
		}
	}
	e := &Engine{bankRaw: append([]byte(nil), bankJSON...), corpusRaw: append([]byte(nil), corpusJSON...), bank: b, corpus: c, challenges: challenges}
	e.bank.Validated = false
	e.bank.Thresholds = nil
	e.bank.ValidationMetrics = nil
	e.report = ValidationReport{SchemaVersion: SchemaVersion, BankVersion: b.Version, BankSHA256: sha(bankJSON), CorpusSHA256: b.CorpusSHA256,
		ChallengeSetSHA256: b.ChallengeSetSHA256, AlgorithmVersion: b.AlgorithmVersion, MethodVersion: b.MethodVersion,
		DatasetKind: b.Kind, ProbabilityReason: "probability_model_not_fitted"}
	groups, reasons, err := readGroups(c, b.Models, challenges)
	if err != nil {
		return nil, ValidationReport{}, err
	}
	e.groups = groups
	for id := range challenges {
		e.challengeIDs = append(e.challengeIDs, id)
	}
	sort.Strings(e.challengeIDs)
	e.report.IntegrityValidated = true
	e.report.Reasons = append(e.report.Reasons, reasons...)
	if options.Policy == nil {
		e.report.Reasons = append(e.report.Reasons, "validation_policy_missing")
	} else {
		p := *options.Policy
		e.report.Policy = &p
	}
	if c.Kind == Synthetic {
		e.report.Reasons = append(e.report.Reasons, "synthetic_mechanics_only")
	}
	if c.Kind == Real {
		e.report.ProvenanceValidated = options.VerifyProvenance != nil && len(c.Records) > 0
		if len(c.Records) == 0 {
			e.report.Reasons = append(e.report.Reasons, "provenance_records_missing")
		}
		if options.VerifyProvenance == nil {
			e.report.Reasons = append(e.report.Reasons, "provenance_verifier_missing")
		} else {
			for _, rec := range c.Records {
				if !evidencePresent(rec.LabelEvidence) || !evidencePresent(rec.RouteEvidence) {
					e.report.ProvenanceValidated = false
					e.report.Reasons = append(e.report.Reasons, "provenance_evidence_missing:"+rec.ID)
					continue
				}
				binding := copyBinding(b.CorpusSHA256, rec, challenges[rec.ChallengeID])
				if err := options.VerifyProvenance(binding); err != nil {
					e.report.ProvenanceValidated = false
					e.report.Reasons = append(e.report.Reasons, "provenance_rejected:"+rec.ID)
				}
			}
		}
	}
	e.report.Reasons = append(e.report.Reasons, e.rebuildCenters()...)
	e.report.Reasons = append(e.report.Reasons, e.deriveThresholds()...)
	e.report.Thresholds = e.thresholds
	e.evaluate()
	return e, e.Report(), nil
}

func identifier(s string) bool { return s != "" && strings.TrimSpace(s) == s }

func validateModels(models []Model) error {
	if len(models) < 2 {
		return fmt.Errorf("at least two distinct known classes are required for replacement and margin comparison")
	}
	seen := map[string]bool{}
	for _, m := range models {
		if !identifier(m.ID) || !identifier(m.Family) || seen[m.ID] {
			return fmt.Errorf("missing or duplicate model/family")
		}
		seen[m.ID] = true
	}
	return nil
}

func challengeHash(c Corpus) string {
	ch := append([]fingerprint.Challenge(nil), c.Challenges...)
	sort.Slice(ch, func(i, j int) bool { return ch[i].ID < ch[j].ID })
	data, _ := json.Marshal(struct {
		SystemPromptSHA256 string                  `json:"system_prompt_sha256"`
		Challenges         []fingerprint.Challenge `json:"challenges"`
	}{c.SystemPromptSHA256, ch})
	return sha(data)
}

func copyBinding(hash string, r Record, c fingerprint.Challenge) Binding {
	r.RequestParameters = append(json.RawMessage(nil), r.RequestParameters...)
	r.LabelEvidence = append(json.RawMessage(nil), r.LabelEvidence...)
	r.RouteEvidence = append(json.RawMessage(nil), r.RouteEvidence...)
	parameters, _ := json.Marshal(c.Parameters)
	c.Parameters = nil
	// The source map was decoded by strictJSON; this clone also preserves exact
	// JSON number tokens rather than rounding seed/parameter integers to float64.
	_ = strictJSON(parameters, &c.Parameters)
	return Binding{CorpusSHA256: hash, Record: r, Challenge: c}
}

func evidencePresent(raw json.RawMessage) bool {
	var value any
	if strictJSON(raw, &value) != nil {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		return len(v) > 0
	case []any:
		return len(v) > 0
	case string:
		return strings.TrimSpace(v) != ""
	}
	return false
}
