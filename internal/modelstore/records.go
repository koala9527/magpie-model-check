package modelstore

import (
	"database/sql"
	"encoding/json"
	"strings"
)

const targetColumns = "id,provider_id,model_id,claimed_model,expected_family,expected_version,enabled,auto_check,interval_minutes,request_budget,last_status,last_checked_at,created_at,updated_at,retry_after"
const batchWriteColumns = "id,target_id,claimed_model,algorithm_version,challenge_version,bank_version,status,route_binding_status,started_at,completed_at,error_message,valid_samples"
const batchColumns = "id,COALESCE(target_id,''),claimed_model,algorithm_version,challenge_version,bank_version,status,route_binding_status,started_at,completed_at,error_message,valid_samples"
const sampleWriteColumns = "id,target_id,batch_id,source_type,challenge_id,parser_version,raw_response,invalid_reason,response_model_field,response_sha256,system_prompt_hash,request_id,collected_at,challenge_index,latency_ms,input_tokens,output_tokens,valid,parsed_numbers_json,features_json,request_params_json,route_evidence_json"
const sampleColumns = "id,COALESCE(target_id,''),batch_id,source_type,challenge_id,parser_version,raw_response,invalid_reason,response_model_field,response_sha256,system_prompt_hash,request_id,collected_at,challenge_index,latency_ms,input_tokens,output_tokens,valid,parsed_numbers_json,features_json,request_params_json,route_evidence_json"
const resultColumns = "id,batch_id,expected_model,predicted_model,predicted_family,identity_status,rejection_reason,bank_version,algorithm_version,created_at,top1_score,top2_score,score_margin,calibrated_probability,candidates_json,metrics_json"
const templateColumns = "id,name,version,prompt,prompt_sha256,parser_version,created_at,parameters_json"
const bankColumns = "id,version,algorithm_version,challenge_version,sha256,source_type,provenance,created_at,model_count,sample_count,trusted,data,validation_metrics_json"

func upsert(tx *sql.Tx, table, columns string, args ...any) error {
	cols := strings.Split(columns, ",")
	updates := make([]string, 0, len(cols)-1)
	for _, col := range cols[1:] {
		updates = append(updates, col+"=excluded."+col)
	}
	_, err := tx.Exec("INSERT INTO "+table+"("+columns+") VALUES("+strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")+") ON CONFLICT(id) DO UPDATE SET "+strings.Join(updates, ","), args...)
	return err
}

func list[T any](q querier, query string, scan func(scanner) (T, error), args ...any) ([]T, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]T, 0)
	for rows.Next() {
		value, err := scan(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func scanTarget(row scanner) (t Target, err error) {
	err = row.Scan(&t.ID, &t.ProviderID, &t.ModelID, &t.ClaimedModel, &t.ExpectedFamily, &t.ExpectedVersion, &t.Enabled, &t.AutoCheck, &t.IntervalMinutes, &t.RequestBudget, &t.LastStatus, &t.LastCheckedAt, &t.CreatedAt, &t.UpdatedAt, &t.RetryAfter)
	return
}

func writeTarget(tx *sql.Tx, t Target) error {
	return upsert(tx, "model_targets", targetColumns, t.ID, t.ProviderID, t.ModelID, t.ClaimedModel, t.ExpectedFamily, t.ExpectedVersion, t.Enabled, t.AutoCheck, t.IntervalMinutes, t.RequestBudget, t.LastStatus, t.LastCheckedAt, t.CreatedAt, t.UpdatedAt, t.RetryAfter)
}

func (s *Store) PutTarget(t Target) (Target, error) {
	var err error
	if t.ID == "" {
		t.ID, err = newID()
		if err != nil {
			return Target{}, err
		}
	}
	if t.ClaimedModel == "" {
		t.ClaimedModel = t.ModelID
	}
	if t.LastStatus == "" {
		t.LastStatus = "PENDING"
	}
	if err = validateTarget(t); err != nil {
		return Target{}, err
	}
	err = s.withTx(func(tx *sql.Tx) error {
		var created string
		err := tx.QueryRow("SELECT created_at FROM model_targets WHERE id=?", t.ID).Scan(&created)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			t.CreatedAt = created
		}
		if t.CreatedAt == "" {
			t.CreatedAt = timestamp()
		}
		t.UpdatedAt = timestamp()
		return writeTarget(tx, t)
	})
	if err != nil {
		return Target{}, err
	}
	return t, nil
}

func (s *Store) Targets() ([]Target, error) {
	return list(s.db, "SELECT "+targetColumns+" FROM model_targets ORDER BY created_at DESC,id", scanTarget)
}

func (s *Store) Target(id string) (Target, error) {
	t, err := scanTarget(s.db.QueryRow("SELECT "+targetColumns+" FROM model_targets WHERE id=?", id))
	return t, missing(err, id)
}

// Removing configuration detaches history; it never deletes the usage ledger.
func (s *Store) DeleteTarget(id string) error {
	return s.withTx(func(tx *sql.Tx) error { return deleteOne(tx, "model_targets", id) })
}

func scanBatch(row scanner) (b Batch, err error) {
	err = row.Scan(&b.ID, &b.TargetID, &b.ClaimedModel, &b.AlgorithmVersion, &b.ChallengeVersion, &b.BankVersion, &b.Status, &b.RouteBindingStatus, &b.StartedAt, &b.CompletedAt, &b.ErrorMessage, &b.ValidSamples)
	return
}

func writeBatch(tx *sql.Tx, b Batch) error {
	return upsert(tx, "fingerprint_batches", batchWriteColumns, b.ID, nullable(b.TargetID), b.ClaimedModel, b.AlgorithmVersion, b.ChallengeVersion, b.BankVersion, b.Status, b.RouteBindingStatus, b.StartedAt, b.CompletedAt, b.ErrorMessage, b.ValidSamples)
}

func (s *Store) PutBatch(b Batch) error {
	if b.StartedAt == "" {
		b.StartedAt = timestamp()
	}
	if b.Status == "" {
		b.Status = "PENDING"
	}
	if err := validateBatch(b); err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error {
		var targetID string
		err := tx.QueryRow("SELECT COALESCE(target_id,'') FROM fingerprint_batches WHERE id=?", b.ID).Scan(&targetID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && targetID != b.TargetID {
			return invalid("existing batch target is immutable")
		}
		return writeBatch(tx, b)
	})
}

func (s *Store) Batches(targetID string, limit int) ([]Batch, error) {
	if limit < 0 {
		return nil, invalid("negative batch limit")
	}
	if limit == 0 {
		limit = -1
	}
	return list(s.db, "SELECT "+batchColumns+" FROM fingerprint_batches WHERE (?='' OR target_id=?) ORDER BY julianday(started_at) DESC,started_at DESC,id LIMIT ?", scanBatch, targetID, targetID, limit)
}

func (s *Store) Batch(id string) (Batch, error) {
	b, err := scanBatch(s.db.QueryRow("SELECT "+batchColumns+" FROM fingerprint_batches WHERE id=?", id))
	return b, missing(err, id)
}

func scanSample(row scanner) (s Sample, err error) {
	var numbers, features, params, route string
	err = row.Scan(&s.ID, &s.TargetID, &s.BatchID, &s.SourceType, &s.ChallengeID, &s.ParserVersion, &s.RawResponse, &s.InvalidReason, &s.ResponseModelField, &s.ResponseSHA256, &s.SystemPromptHash, &s.RequestID, &s.CollectedAt, &s.ChallengeIndex, &s.LatencyMS, &s.InputTokens, &s.OutputTokens, &s.Valid, &numbers, &features, &params, &route)
	if err != nil {
		return
	}
	for _, field := range []struct {
		raw   string
		value any
	}{{numbers, &s.ParsedNumbers}, {features, &s.Features}, {params, &s.RequestParams}, {route, &s.RouteEvidence}} {
		if err = strictDecode([]byte(field.raw), field.value); err != nil {
			return
		}
	}
	return
}

func writeSample(tx *sql.Tx, s Sample) error {
	numbers, err := publicJSON(s.ParsedNumbers)
	if err != nil {
		return err
	}
	features, err := publicJSON(s.Features)
	if err != nil {
		return err
	}
	params, err := publicJSON(s.RequestParams)
	if err != nil {
		return err
	}
	route, err := publicJSON(s.RouteEvidence)
	if err != nil {
		return err
	}
	return upsert(tx, "fingerprint_samples", sampleWriteColumns, s.ID, nullable(s.TargetID), s.BatchID, s.SourceType, s.ChallengeID, s.ParserVersion, s.RawResponse, s.InvalidReason, s.ResponseModelField, s.ResponseSHA256, s.SystemPromptHash, s.RequestID, s.CollectedAt, s.ChallengeIndex, s.LatencyMS, s.InputTokens, s.OutputTokens, s.Valid, numbers, features, params, route)
}

func (s *Store) PutSample(sample Sample) error {
	if sample.CollectedAt == "" {
		sample.CollectedAt = timestamp()
	}
	if sample.ResponseSHA256 == "" {
		sample.ResponseSHA256 = sha([]byte(sample.RawResponse))
	}
	if err := validateSample(sample); err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error {
		var targetID string
		if err := tx.QueryRow("SELECT COALESCE(target_id,'') FROM fingerprint_batches WHERE id=?", sample.BatchID).Scan(&targetID); err != nil {
			return missing(err, sample.BatchID)
		}
		if sample.TargetID == "" {
			sample.TargetID = targetID
		}
		if sample.TargetID != targetID {
			return invalid("sample target differs from batch target")
		}
		return writeSample(tx, sample)
	})
}

func (s *Store) Samples(batchID string) ([]Sample, error) {
	return list(s.db, "SELECT "+sampleColumns+" FROM fingerprint_samples WHERE batch_id=? ORDER BY challenge_index,id", scanSample, batchID)
}

func scanResult(row scanner) (r Result, err error) {
	var candidates, metrics string
	err = row.Scan(&r.ID, &r.BatchID, &r.ExpectedModel, &r.PredictedModel, &r.PredictedFamily, &r.IdentityStatus, &r.RejectionReason, &r.BankVersion, &r.AlgorithmVersion, &r.CreatedAt, &r.Top1Score, &r.Top2Score, &r.ScoreMargin, &r.CalibratedProbability, &candidates, &metrics)
	if err != nil {
		return
	}
	if err = strictDecode([]byte(candidates), &r.Candidates); err != nil {
		return
	}
	err = strictDecode([]byte(metrics), &r.Metrics)
	return
}

func writeResult(tx *sql.Tx, r Result) error {
	candidates, err := publicJSON(r.Candidates)
	if err != nil {
		return err
	}
	metrics, err := publicJSON(r.Metrics)
	if err != nil {
		return err
	}
	return upsert(tx, "attribution_results", resultColumns, r.ID, r.BatchID, r.ExpectedModel, r.PredictedModel, r.PredictedFamily, r.IdentityStatus, r.RejectionReason, r.BankVersion, r.AlgorithmVersion, r.CreatedAt, r.Top1Score, r.Top2Score, r.ScoreMargin, r.CalibratedProbability, candidates, metrics)
}

func (s *Store) PutResult(r Result) error {
	if r.CreatedAt == "" {
		r.CreatedAt = timestamp()
	}
	if err := validateResult(r); err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error { return writeResult(tx, r) })
}

func (s *Store) Result(batchID string) (Result, error) {
	r, err := scanResult(s.db.QueryRow("SELECT "+resultColumns+" FROM attribution_results WHERE batch_id=?", batchID))
	return r, missing(err, batchID)
}

func scanTemplate(row scanner) (t Template, err error) {
	var params string
	err = row.Scan(&t.ID, &t.Name, &t.Version, &t.Prompt, &t.PromptSHA256, &t.ParserVersion, &t.CreatedAt, &params)
	if err == nil {
		err = strictDecode([]byte(params), &t.Parameters)
	}
	return
}

func writeTemplate(tx *sql.Tx, t Template) error {
	params, err := publicJSON(t.Parameters)
	if err != nil {
		return err
	}
	return upsert(tx, "challenge_templates", templateColumns, t.ID, t.Name, t.Version, t.Prompt, t.PromptSHA256, t.ParserVersion, t.CreatedAt, params)
}

func (s *Store) PutTemplate(t Template) error {
	if t.CreatedAt == "" {
		t.CreatedAt = timestamp()
	}
	if t.PromptSHA256 == "" {
		t.PromptSHA256 = sha([]byte(t.Prompt))
	}
	if t.Version == 0 {
		t.Version = 1
	}
	if err := validateTemplate(t); err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error { return writeTemplate(tx, t) })
}

func (s *Store) Templates() ([]Template, error) {
	return list(s.db, "SELECT "+templateColumns+" FROM challenge_templates ORDER BY created_at,id", scanTemplate)
}

func scanBank(row scanner) (b Bank, err error) {
	var data []byte
	var metrics string
	err = row.Scan(&b.ID, &b.Version, &b.AlgorithmVersion, &b.ChallengeVersion, &b.SHA256, &b.SourceType, &b.Provenance, &b.CreatedAt, &b.ModelCount, &b.SampleCount, &b.Trusted, &data, &metrics)
	if err != nil {
		return
	}
	b.Data = json.RawMessage(data)
	err = strictDecode([]byte(metrics), &b.ValidationMetrics)
	return
}

func writeBank(tx *sql.Tx, b Bank) error {
	metrics, err := publicJSON(b.ValidationMetrics)
	if err != nil {
		return err
	}
	return upsert(tx, "reference_banks", bankColumns, b.ID, b.Version, b.AlgorithmVersion, b.ChallengeVersion, b.SHA256, b.SourceType, b.Provenance, b.CreatedAt, b.ModelCount, b.SampleCount, b.Trusted, []byte(b.Data), metrics)
}

func (s *Store) PutBank(b Bank) error {
	if b.CreatedAt == "" {
		b.CreatedAt = timestamp()
	}
	if b.SHA256 == "" {
		b.SHA256 = sha(b.Data)
	}
	if err := validateBank(b); err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error { return writeBank(tx, b) })
}

func (s *Store) Banks() ([]Bank, error) {
	return list(s.db, "SELECT "+bankColumns+" FROM reference_banks ORDER BY created_at,id", scanBank)
}

func (s *Store) Bank(id string) (Bank, error) {
	b, err := scanBank(s.db.QueryRow("SELECT "+bankColumns+" FROM reference_banks WHERE id=?", id))
	return b, missing(err, id)
}

func (s *Store) DeleteBank(id string) error {
	return s.withTx(func(tx *sql.Tx) error { return deleteOne(tx, "reference_banks", id) })
}

func scanLegacy(row scanner) (r LegacyRecord, err error) {
	var data []byte
	err = row.Scan(&r.ID, &r.Model, &r.Time, &r.Verdict, &data)
	if err == nil {
		r.Data = json.RawMessage(data)
	}
	return
}

func (s *Store) Legacy() ([]LegacyRecord, error) {
	return list(s.db, "SELECT id,model,time,verdict,data FROM legacy_history ORDER BY time DESC,id", scanLegacy)
}
