package attribution

import (
	"encoding/json"

	"github.com/koala9527/magpie-model-check/internal/fingerprint"
)

// Engine owns decoded inputs and recomputed results. No caller-owned derived
// fields or import-validation flag are retained as a trust decision.
type Engine struct {
	bankRaw      []byte
	corpusRaw    []byte
	report       ValidationReport
	bank         Bank
	corpus       Corpus
	challenges   map[string]fingerprint.Challenge
	groups       []group
	challengeIDs []string
	centers      map[string]map[string][]float64
	thresholds   map[string]Threshold
}

// BuiltinLegacy preserves the original ModelTrace export without attesting to
// its labels, reference independence, thresholds or identity performance.
func BuiltinLegacy() (*Engine, ValidationReport) {
	e := &Engine{
		bankRaw: fingerprint.LegacyBankJSON(),
		report: ValidationReport{
			SchemaVersion:      SchemaVersion,
			BankVersion:        "modeltrace-6cd4a8ff-legacy",
			BankSHA256:         fingerprint.LegacyBankSHA256(),
			AlgorithmVersion:   fingerprint.AlgorithmVersion,
			MethodVersion:      MethodVersion,
			IntegrityValidated: true,
			Reasons:            []string{"legacy_bank_unvalidated", "raw_reference_corpus_missing", "independent_calibration_missing", "independent_holdouts_missing"},
			ProbabilityReason:  "probability_model_not_fitted",
		},
	}
	return e, e.Report()
}

// Report returns a deep copy. Modifying it cannot calibrate or validate Engine.
func (e *Engine) Report() ValidationReport {
	if e == nil {
		return ValidationReport{Reasons: []string{"engine_missing"}, ProbabilityReason: "probability_model_not_fitted"}
	}
	data, err := json.Marshal(e.report)
	if err != nil {
		panic("internal attribution report is not JSON serializable: " + err.Error())
	}
	var out ValidationReport
	if err := json.Unmarshal(data, &out); err != nil {
		panic("internal attribution report clone failed: " + err.Error())
	}
	return out
}

// RawBankJSON returns the exact manifest bytes (or original legacy bank).
func (e *Engine) RawBankJSON() []byte {
	if e == nil {
		return nil
	}
	return append([]byte(nil), e.bankRaw...)
}

// RawCorpusJSON returns the exact bound corpus bytes, not reserialized JSON.
func (e *Engine) RawCorpusJSON() []byte {
	if e == nil {
		return nil
	}
	return append([]byte(nil), e.corpusRaw...)
}

func (e *Engine) Decide(request Request) Decision {
	d := e.decideMechanics(request)
	if e == nil {
		return d
	}
	if !e.report.IdentityValidated {
		d.Status = Inconclusive
		d.PredictedModel = ""
		d.PredictedFamily = ""
		d.Reasons = append(d.Reasons, e.report.Reasons...)
		if len(d.Reasons) == 0 {
			d.Reasons = append(d.Reasons, "identity_validation_incomplete")
		}
	} else {
		d.IdentityValidated = d.Status != Inconclusive
	}
	return d
}
