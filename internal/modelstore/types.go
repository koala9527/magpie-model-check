// Package modelstore persists model-check data without importing the detection
// service or Magpie integration. All timestamps use RFC3339 and usage days use
// YYYY-MM-DD. A reservation is final; no history operation refunds it.
package modelstore

import (
	"encoding/json"
	"errors"
)

var (
	ErrNotFound       = errors.New("modelstore: not found")
	ErrBudgetExceeded = errors.New("modelstore: daily budget exceeded")
	ErrInvalidData    = errors.New("modelstore: invalid data")
)

const SchemaVersion = 1

// DefaultSettings returns a fresh value, not shared mutable configuration.
func DefaultSettings() Settings {
	return Settings{DailyRequests: 60, DailyTokens: 300000, Concurrency: 2, TimeoutSeconds: 30, RetentionDays: 30}
}

type Target struct {
	ID              string `json:"id"`
	ProviderID      string `json:"provider_id"`
	ModelID         string `json:"model_id"`
	ClaimedModel    string `json:"claimed_model"`
	ExpectedFamily  string `json:"expected_family"`
	ExpectedVersion string `json:"expected_version"`
	Enabled         bool   `json:"enabled"`
	AutoCheck       bool   `json:"auto_check"`
	IntervalMinutes int    `json:"interval_minutes"`
	RequestBudget   int    `json:"request_budget"`
	LastStatus      string `json:"last_status"`
	LastCheckedAt   string `json:"last_checked_at"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	RetryAfter      string `json:"retry_after"`
}

type Settings struct {
	DailyRequests  int `json:"daily_requests"`
	DailyTokens    int `json:"daily_tokens"`
	Concurrency    int `json:"concurrency"`
	TimeoutSeconds int `json:"timeout_seconds"`
	RetentionDays  int `json:"retention_days"`
}

type Usage struct {
	Day      string `json:"day"`
	Requests int    `json:"requests"`
	Tokens   int    `json:"tokens"`
}

type Sample struct {
	ID                 string         `json:"id"`
	TargetID           string         `json:"target_id"`
	BatchID            string         `json:"batch_id"`
	SourceType         string         `json:"source_type"`
	ChallengeID        string         `json:"challenge_id"`
	ParserVersion      string         `json:"parser_version"`
	RawResponse        string         `json:"raw_response"`
	InvalidReason      string         `json:"invalid_reason"`
	ResponseModelField string         `json:"response_model_field"`
	ResponseSHA256     string         `json:"response_sha256,omitempty"`
	SystemPromptHash   string         `json:"system_prompt_hash,omitempty"`
	RequestID          string         `json:"request_id,omitempty"`
	CollectedAt        string         `json:"collected_at"`
	ChallengeIndex     int            `json:"challenge_index"`
	LatencyMS          int            `json:"latency_ms"`
	InputTokens        int            `json:"input_tokens"`
	OutputTokens       int            `json:"output_tokens"`
	Valid              bool           `json:"valid"`
	ParsedNumbers      []int          `json:"parsed_numbers"`
	Features           []float64      `json:"features"`
	RequestParams      map[string]any `json:"request_params"`
	RouteEvidence      map[string]any `json:"route_evidence"`
}

type Batch struct {
	ID                 string `json:"id"`
	TargetID           string `json:"target_id"`
	ClaimedModel       string `json:"claimed_model"`
	AlgorithmVersion   string `json:"algorithm_version"`
	ChallengeVersion   string `json:"challenge_version"`
	BankVersion        string `json:"bank_version"`
	Status             string `json:"status"`
	RouteBindingStatus string `json:"route_binding_status"`
	StartedAt          string `json:"started_at"`
	CompletedAt        string `json:"completed_at"`
	ErrorMessage       string `json:"error_message"`
	ValidSamples       int    `json:"valid_samples"`
}

type Result struct {
	ID                    string           `json:"id"`
	BatchID               string           `json:"batch_id"`
	ExpectedModel         string           `json:"expected_model"`
	PredictedModel        string           `json:"predicted_model"`
	PredictedFamily       string           `json:"predicted_family"`
	IdentityStatus        string           `json:"identity_status"`
	RejectionReason       string           `json:"rejection_reason"`
	BankVersion           string           `json:"bank_version"`
	AlgorithmVersion      string           `json:"algorithm_version"`
	CreatedAt             string           `json:"created_at"`
	Top1Score             *float64         `json:"top1_score"`
	Top2Score             *float64         `json:"top2_score"`
	ScoreMargin           *float64         `json:"score_margin"`
	CalibratedProbability *float64         `json:"calibrated_probability"`
	Candidates            []map[string]any `json:"candidates"`
	Metrics               map[string]any   `json:"metrics"`
}

type Template struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Prompt        string         `json:"prompt"`
	PromptSHA256  string         `json:"prompt_sha256"`
	ParserVersion string         `json:"parser_version"`
	CreatedAt     string         `json:"created_at"`
	Version       int            `json:"version"`
	Parameters    map[string]any `json:"parameters"`
}

type Bank struct {
	ID                string          `json:"id"`
	Version           string          `json:"version"`
	AlgorithmVersion  string          `json:"algorithm_version"`
	ChallengeVersion  string          `json:"challenge_version"`
	SHA256            string          `json:"sha256"`
	SourceType        string          `json:"source_type"`
	Provenance        string          `json:"provenance"`
	CreatedAt         string          `json:"created_at"`
	ModelCount        int             `json:"model_count"`
	SampleCount       int             `json:"sample_count"`
	Trusted           bool            `json:"trusted"`
	Data              json.RawMessage `json:"data"`
	ValidationMetrics map[string]any  `json:"validation_metrics"`
}

// LegacyRecord is quality-check history, never an identity attribution result.
// Data contains the original history record, not the old state/config object.
type LegacyRecord struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Time    string          `json:"time"`
	Verdict string          `json:"verdict"`
	Data    json.RawMessage `json:"data"`
}
