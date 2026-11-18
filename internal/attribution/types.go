// Package attribution validates versioned behavioral reference experiments.
// It does not turn closed-set ModelTrace scores into identity probabilities.
package attribution

import (
	"encoding/json"

	"github.com/koala9527/magpie-model-check/internal/fingerprint"
)

const SchemaVersion = 1
const MethodVersion = "hellinger-reference-centroid-v1"
const BankFormat = "magpie-attribution-bank"

type DatasetKind string

const (
	Real      DatasetKind = "real"
	Synthetic DatasetKind = "synthetic"
)

type Split string

const (
	Reference      Split = "reference"
	Calibration    Split = "calibration"
	Test           Split = "test"
	UnknownTest    Split = "unknown_test"
	ChannelHoldout Split = "channel_holdout"
	TimeHoldout    Split = "time_holdout"
)

type Status string

const (
	Matched      Status = "MATCHED"
	Mismatched   Status = "MISMATCHED"
	Unknown      Status = "UNKNOWN"
	Inconclusive Status = "INCONCLUSIVE"
)

type Model struct {
	ID     string `json:"id"`
	Family string `json:"family"`
}

// Bank is a manifest. Imported derived fields are deliberately never trusted.
type Bank struct {
	Format             string               `json:"format"`
	SchemaVersion      int                  `json:"schema_version"`
	Version            string               `json:"version"`
	AlgorithmVersion   string               `json:"algorithm_version"`
	MethodVersion      string               `json:"method_version"`
	CorpusSHA256       string               `json:"corpus_sha256"`
	ChallengeSetSHA256 string               `json:"challenge_set_sha256"`
	Kind               DatasetKind          `json:"kind"`
	Models             []Model              `json:"models"`
	SourceType         string               `json:"source_type"`
	Validated          bool                 `json:"validated"`
	Thresholds         map[string]Threshold `json:"thresholds,omitempty"`
	ValidationMetrics  json.RawMessage      `json:"validation_metrics,omitempty"`
}

// CorpusSHA256 binds this whole document's original bytes, not reserialized JSON.
type Corpus struct {
	SchemaVersion      int                     `json:"schema_version"`
	AlgorithmVersion   string                  `json:"algorithm_version"`
	Kind               DatasetKind             `json:"kind"`
	SystemPromptSHA256 string                  `json:"system_prompt_sha256"`
	Challenges         []fingerprint.Challenge `json:"challenges"`
	Records            []Record                `json:"records"`
}

// Record is one challenge in an independently collected complete group.
// No parsed numbers, feature vectors or imported validity flag are accepted.
type Record struct {
	ID                  string          `json:"id"`
	GroupID             string          `json:"group_id"`
	BatchID             string          `json:"batch_id"`
	SessionID           string          `json:"session_id"`
	Split               Split           `json:"split"`
	Model               string          `json:"model"`
	Family              string          `json:"family"`
	Channel             string          `json:"channel"`
	CollectedAt         string          `json:"collected_at"`
	SourceType          string          `json:"source_type"`
	ChallengeID         string          `json:"challenge_id"`
	RawResponse         string          `json:"raw_response"`
	ResponseSHA256      string          `json:"response_sha256"`
	FinishReason        string          `json:"finish_reason"`
	RequestID           string          `json:"request_id"`
	RequestParameters   json.RawMessage `json:"request_parameters"`
	SystemPromptSHA256  string          `json:"system_prompt_sha256"`
	LabelEvidence       json.RawMessage `json:"label_evidence"`
	LabelEvidenceSHA256 string          `json:"label_evidence_sha256"`
	RouteEvidence       json.RawMessage `json:"route_evidence"`
	RouteEvidenceSHA256 string          `json:"route_evidence_sha256"`
}

// Binding is supplied to a trusted external auditor, not read from an import.
// Record is a defensive copy and CorpusSHA256 binds all surrounding metadata.
type Binding struct {
	CorpusSHA256 string                `json:"corpus_sha256"`
	Record       Record                `json:"record"`
	Challenge    fingerprint.Challenge `json:"challenge"`
}

type Options struct {
	Policy           *Policy
	VerifyProvenance func(Binding) error
}

// Policy specifies experimental release targets, not fitted thresholds.
// There are no default targets or universally sufficient fixed sample counts.
type Policy struct {
	Confidence              float64 `json:"confidence"`
	MaxFalseMatchRate       float64 `json:"max_false_match_rate"`
	MaxFalseReplacementRate float64 `json:"max_false_replacement_rate"`
	MinUnknownRecall        float64 `json:"min_unknown_recall"`
	MaxKnownRejectionRate   float64 `json:"max_known_rejection_rate"`
	MinTop1Accuracy         float64 `json:"min_top1_accuracy"`
	MinFamilyAccuracy       float64 `json:"min_family_accuracy"`
}

type Interval struct {
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

type Metric struct {
	Events     int       `json:"events"`
	Total      int       `json:"total"`
	Value      *float64  `json:"value"`
	Confidence float64   `json:"confidence"`
	Interval   *Interval `json:"wilson_ci"`
}

type Metrics struct {
	Groups               int                       `json:"independent_groups"`
	Top1Accuracy         Metric                    `json:"top1_accuracy"`
	FamilyAccuracy       Metric                    `json:"family_accuracy"`
	FalseMatchRate       Metric                    `json:"false_match_rate"`
	FalseReplacementRate Metric                    `json:"false_replacement_rate"`
	UnknownRecall        Metric                    `json:"unknown_recall"`
	KnownRejectionRate   Metric                    `json:"known_class_rejection_rate"`
	BrierScore           *float64                  `json:"brier_score"`
	CalibrationError     *float64                  `json:"calibration_error"`
	Confusion            map[string]map[string]int `json:"confusion"`
}

type Threshold struct {
	MaxDistance          float64            `json:"max_distance"`
	MinMargin            float64            `json:"min_margin"`
	MinConsistency       float64            `json:"min_consistency"`
	MaxChallengeDistance map[string]float64 `json:"max_challenge_distance"`
	CalibrationGroups    int                `json:"calibration_groups"`
}

type Candidate struct {
	Model       string  `json:"model"`
	Family      string  `json:"family"`
	Score       float64 `json:"score"`
	Distance    float64 `json:"distance"`
	Consistency float64 `json:"consistency"`
}

type Observation struct {
	ChallengeID       string          `json:"challenge_id"`
	RawResponse       string          `json:"raw_response"`
	FinishReason      string          `json:"finish_reason"`
	RequestParameters json.RawMessage `json:"request_parameters,omitempty"`
}

// RouteVerified is set by the collector after authenticating route evidence.
// It is not derived from bank/corpus fields or response model labels.
type Request struct {
	ClaimedModel       string          `json:"claimed_model"`
	BankVersion        string          `json:"bank_version"`
	AlgorithmVersion   string          `json:"algorithm_version"`
	ChallengeSetSHA256 string          `json:"challenge_set_sha256"`
	Channel            string          `json:"channel"`
	CollectedAt        string          `json:"collected_at"`
	RouteVerified      bool            `json:"route_verified"`
	SystemPromptSHA256 string          `json:"system_prompt_sha256"`
	RequestParameters  json.RawMessage `json:"request_parameters"`
	Observations       []Observation   `json:"observations"`
}

type Decision struct {
	Status                Status      `json:"identity_status"`
	Reasons               []string    `json:"reasons"`
	ExpectedModel         string      `json:"expected_model"`
	PredictedModel        string      `json:"predicted_model"`
	PredictedFamily       string      `json:"predicted_family"`
	Candidates            []Candidate `json:"candidates"`
	CalibratedProbability *float64    `json:"calibrated_probability"`
	BankVersion           string      `json:"bank_version"`
	AlgorithmVersion      string      `json:"algorithm_version"`
	MethodVersion         string      `json:"method_version"`
	CorpusSHA256          string      `json:"corpus_sha256"`
	DatasetKind           DatasetKind `json:"dataset_kind"`
	IdentityValidated     bool        `json:"identity_validated"`
}

type ValidationReport struct {
	SchemaVersion       int                          `json:"schema_version"`
	BankVersion         string                       `json:"bank_version"`
	BankSHA256          string                       `json:"bank_sha256"`
	CorpusSHA256        string                       `json:"corpus_sha256"`
	ChallengeSetSHA256  string                       `json:"challenge_set_sha256"`
	AlgorithmVersion    string                       `json:"algorithm_version"`
	MethodVersion       string                       `json:"method_version"`
	DatasetKind         DatasetKind                  `json:"dataset_kind"`
	IntegrityValidated  bool                         `json:"integrity_validated"`
	ProvenanceValidated bool                         `json:"provenance_validated"`
	MechanicsValidated  bool                         `json:"mechanics_validated"`
	IdentityValidated   bool                         `json:"identity_validated"`
	Reasons             []string                     `json:"reasons"`
	Policy              *Policy                      `json:"policy"`
	Thresholds          map[string]Threshold         `json:"recomputed_thresholds"`
	BySplit             map[Split]Metrics            `json:"by_split"`
	ByModel             map[string]Metrics           `json:"by_model"`
	ByModelSplit        map[Split]map[string]Metrics `json:"by_model_split"`
	ByChannel           map[string]Metrics           `json:"by_channel"`
	ByDate              map[string]Metrics           `json:"by_utc_date"`
	ProbabilityReason   string                       `json:"probability_reason"`
}
