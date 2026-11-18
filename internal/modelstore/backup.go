package modelstore

import (
	"bytes"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"time"
)

const ExportFormat = "magpie-model-check.modelstore"
const maxRestoreBytes = 128 << 20

// JSON blobs with byte-level hashes are exported as JSON strings, not embedded
// objects: encoding/json compacts RawMessage, which would otherwise change
// original whitespace and invalidate the bank/snapshot provenance hashes.
type backupBank struct {
	Bank
	Data string `json:"data"`
}
type backupLegacy struct {
	LegacyRecord
	Data string `json:"data"`
}
type metaRecord struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type backupTables struct {
	Targets   []Target       `json:"model_targets"`
	Templates []Template     `json:"challenge_templates"`
	Samples   []Sample       `json:"fingerprint_samples"`
	Batches   []Batch        `json:"fingerprint_batches"`
	Results   []Result       `json:"attribution_results"`
	Banks     []backupBank   `json:"reference_banks"`
	Settings  Settings       `json:"store_settings"`
	Usage     []Usage        `json:"daily_usage"`
	Legacy    []backupLegacy `json:"legacy_history"`
	Meta      []metaRecord   `json:"store_meta"`
}

type backupDocument struct {
	Format string       `json:"format"`
	Schema int          `json:"schema"`
	Tables backupTables `json:"tables"`
	Hash   string       `json:"hash"`
}

func documentHash(d backupDocument) (string, error) {
	payload := map[string]any{"format": d.Format, "schema": d.Schema, "tables": d.Tables}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	// Canonical object-key ordering throughout; UseNumber avoids rounding large
	// int64 ledger values through float64 during checksum calculation.
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	var canonical any
	if err = decoder.Decode(&canonical); err != nil {
		return "", err
	}
	b, err = json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return sha(b), nil
}

func exportFrom(tx *sql.Tx) ([]byte, error) {
	d := backupDocument{Format: ExportFormat, Schema: SchemaVersion}
	t := &d.Tables
	var err error
	if t.Targets, err = list(tx, "SELECT "+targetColumns+" FROM model_targets ORDER BY id", scanTarget); err != nil {
		return nil, err
	}
	if t.Templates, err = list(tx, "SELECT "+templateColumns+" FROM challenge_templates ORDER BY id", scanTemplate); err != nil {
		return nil, err
	}
	if t.Samples, err = list(tx, "SELECT "+sampleColumns+" FROM fingerprint_samples ORDER BY id", scanSample); err != nil {
		return nil, err
	}
	if t.Batches, err = list(tx, "SELECT "+batchColumns+" FROM fingerprint_batches ORDER BY id", scanBatch); err != nil {
		return nil, err
	}
	if t.Results, err = list(tx, "SELECT "+resultColumns+" FROM attribution_results ORDER BY id", scanResult); err != nil {
		return nil, err
	}
	banks, err := list(tx, "SELECT "+bankColumns+" FROM reference_banks ORDER BY id", scanBank)
	if err != nil {
		return nil, err
	}
	t.Banks = make([]backupBank, 0, len(banks))
	for _, bank := range banks {
		t.Banks = append(t.Banks, backupBank{Bank: bank, Data: string(bank.Data)})
	}
	if t.Settings, err = settingsFrom(tx); err != nil {
		return nil, err
	}
	if t.Usage, err = list(tx, "SELECT day,requests,tokens FROM daily_usage ORDER BY day", func(row scanner) (u Usage, err error) { err = row.Scan(&u.Day, &u.Requests, &u.Tokens); return }); err != nil {
		return nil, err
	}
	history, err := list(tx, "SELECT id,model,time,verdict,data FROM legacy_history ORDER BY id", scanLegacy)
	if err != nil {
		return nil, err
	}
	t.Legacy = make([]backupLegacy, 0, len(history))
	for _, record := range history {
		t.Legacy = append(t.Legacy, backupLegacy{LegacyRecord: record, Data: string(record.Data)})
	}
	if t.Meta, err = list(tx, "SELECT key,value FROM store_meta ORDER BY key", func(row scanner) (m metaRecord, err error) { err = row.Scan(&m.Key, &m.Value); return }); err != nil {
		return nil, err
	}
	if d.Hash, err = documentHash(d); err != nil {
		return nil, err
	}
	return json.Marshal(d)
}

// Export is a deterministic, transactionally consistent logical backup. It
// contains no boot config, credentials, raw legacy state or SQLite sidecars.
func (s *Store) Export() ([]byte, error) {
	var data []byte
	err := s.withTx(func(tx *sql.Tx) error { var err error; data, err = exportFrom(tx); return err })
	return data, err
}

func requireFields(raw json.RawMessage, zero any) error {
	encoded, err := json.Marshal(zero)
	if err != nil {
		return err
	}
	var expected, actual map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &expected); err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &actual); err != nil || actual == nil {
		return invalid("row/settings must be an object")
	}
	for key, zero := range expected {
		value, ok := actual[key]
		if !ok {
			return invalid("missing field %s", key)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) && !bytes.Equal(bytes.TrimSpace(zero), []byte("null")) {
			return invalid("null non-nullable field %s", key)
		}
	}
	return nil
}

func validateTableFields(raw map[string]json.RawMessage) error {
	rows := map[string]any{
		"model_targets": Target{}, "challenge_templates": Template{}, "fingerprint_samples": Sample{},
		"fingerprint_batches": Batch{}, "attribution_results": Result{}, "reference_banks": backupBank{},
		"daily_usage": Usage{}, "legacy_history": backupLegacy{}, "store_meta": metaRecord{},
	}
	if len(raw) != len(rows)+1 {
		return invalid("missing/unknown table")
	}
	for key, zero := range rows {
		body, ok := raw[key]
		if !ok || bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
			return invalid("missing/null table %s", key)
		}
		var records []json.RawMessage
		if err := json.Unmarshal(body, &records); err != nil {
			return invalid("table %s must be an array", key)
		}
		for _, record := range records {
			if err := requireFields(record, zero); err != nil {
				return err
			}
			if key == "fingerprint_samples" {
				if err := validateSampleJSONTypes(record); err != nil {
					return err
				}
			}
		}
	}
	if body, ok := raw["store_settings"]; !ok {
		return invalid("missing settings")
	} else {
		return requireFields(body, Settings{})
	}
}

func validateSampleJSONTypes(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return invalid("invalid sample object")
	}
	for _, key := range []string{"response_sha256", "system_prompt_hash", "request_id"} {
		if value, ok := fields[key]; ok && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid("null optional string %s", key)
		}
	}
	for _, key := range []string{"parsed_numbers", "features"} {
		var numbers []json.RawMessage
		if err := json.Unmarshal(fields[key], &numbers); err != nil {
			return invalid("invalid sample numeric array")
		}
		for _, number := range numbers {
			if bytes.Equal(bytes.TrimSpace(number), []byte("null")) {
				return invalid("null sample array number")
			}
		}
	}
	return nil
}

func validateBackup(data []byte) (backupDocument, error) {
	var d backupDocument
	if len(data) == 0 || len(data) > maxRestoreBytes {
		return d, invalid("empty/oversized backup")
	}
	if err := strictDecode(data, &d); err != nil {
		return d, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil || len(top) != 4 {
		return d, invalid("missing document fields")
	}
	var rawTables map[string]json.RawMessage
	if err := json.Unmarshal(top["tables"], &rawTables); err != nil {
		return d, invalid("invalid tables")
	}
	if err := validateTableFields(rawTables); err != nil {
		return d, err
	}
	if d.Format != ExportFormat || d.Schema != SchemaVersion || !validSHA(d.Hash) {
		return d, invalid("unsupported format/schema/hash")
	}
	expected, err := documentHash(d)
	if err != nil {
		return d, err
	}
	if subtle.ConstantTimeCompare([]byte(d.Hash), []byte(expected)) != 1 {
		return d, invalid("backup hash mismatch")
	}
	if err = validateSettings(d.Tables.Settings); err != nil {
		return d, err
	}
	targetIDs := map[string]bool{}
	for _, target := range d.Tables.Targets {
		if targetIDs[target.ID] {
			return d, invalid("duplicate target ID")
		}
		targetIDs[target.ID] = true
		if err = validateTarget(target); err != nil {
			return d, err
		}
		if target.CreatedAt == "" || target.UpdatedAt == "" {
			return d, invalid("target timestamp missing")
		}
	}
	batchIDs := map[string]Batch{}
	for _, batch := range d.Tables.Batches {
		if _, ok := batchIDs[batch.ID]; ok {
			return d, invalid("duplicate batch ID")
		}
		batchIDs[batch.ID] = batch
		if batch.TargetID != "" && !targetIDs[batch.TargetID] {
			return d, invalid("orphan batch target")
		}
		if err = validateBatch(batch); err != nil {
			return d, err
		}
		if batch.StartedAt == "" {
			return d, invalid("batch timestamp missing")
		}
	}
	ids := map[string]bool{}
	for _, sample := range d.Tables.Samples {
		if ids[sample.ID] {
			return d, invalid("duplicate sample ID")
		}
		ids[sample.ID] = true
		batch, ok := batchIDs[sample.BatchID]
		if !ok || sample.TargetID != batch.TargetID {
			return d, invalid("orphan/mismatched sample")
		}
		if err = validateSample(sample); err != nil {
			return d, err
		}
		if sample.CollectedAt == "" {
			return d, invalid("sample timestamp missing")
		}
	}
	ids, resultBatches := map[string]bool{}, map[string]bool{}
	for _, result := range d.Tables.Results {
		if ids[result.ID] || resultBatches[result.BatchID] {
			return d, invalid("duplicate result ID/batch")
		}
		ids[result.ID], resultBatches[result.BatchID] = true, true
		if _, ok := batchIDs[result.BatchID]; !ok {
			return d, invalid("orphan result")
		}
		if err = validateResult(result); err != nil {
			return d, err
		}
		if result.CreatedAt == "" {
			return d, invalid("result timestamp missing")
		}
	}
	ids = map[string]bool{}
	for _, template := range d.Tables.Templates {
		if ids[template.ID] {
			return d, invalid("duplicate template ID")
		}
		ids[template.ID] = true
		if err = validateTemplate(template); err != nil {
			return d, err
		}
		if template.CreatedAt == "" {
			return d, invalid("template timestamp missing")
		}
	}
	ids = map[string]bool{}
	for i := range d.Tables.Banks {
		b := &d.Tables.Banks[i]
		b.Bank.Data = json.RawMessage(b.Data)
		if ids[b.ID] {
			return d, invalid("duplicate bank ID")
		}
		ids[b.ID] = true
		if err = validateBank(b.Bank); err != nil {
			return d, err
		}
		if b.CreatedAt == "" {
			return d, invalid("bank timestamp missing")
		}
	}
	ids = map[string]bool{}
	for _, usage := range d.Tables.Usage {
		if ids[usage.Day] {
			return d, invalid("duplicate usage day")
		}
		ids[usage.Day] = true
		if err = validDay(usage.Day); err != nil {
			return d, err
		}
		if usage.Requests < 0 || usage.Tokens < 0 {
			return d, invalid("negative usage")
		}
	}
	ids = map[string]bool{}
	for i := range d.Tables.Legacy {
		r := &d.Tables.Legacy[i]
		r.LegacyRecord.Data = json.RawMessage(r.Data)
		if ids[r.ID] {
			return d, invalid("duplicate legacy ID")
		}
		ids[r.ID] = true
		if err = validID(r.ID); err != nil {
			return d, err
		}
		if err = validTimes(r.Time); err != nil {
			return d, err
		}
		if err = checkJSON(r.LegacyRecord.Data, true); err != nil {
			return d, err
		}
		if len(bytes.TrimSpace(r.LegacyRecord.Data)) == 0 || bytes.TrimSpace(r.LegacyRecord.Data)[0] != '{' {
			return d, invalid("legacy history must be an object")
		}
	}
	ids = map[string]bool{}
	for _, meta := range d.Tables.Meta {
		if meta.Key != legacyMarker || ids[meta.Key] {
			return d, invalid("unknown/duplicate metadata")
		}
		ids[meta.Key] = true
		var manifest migrationManifest
		if err = strictDecode([]byte(meta.Value), &manifest); err != nil {
			return d, err
		}
		if !validSHA(manifest.SourceSHA256) || !validSHA(manifest.SnapshotSHA256) || !validSHA(manifest.StateSHA256) || manifest.TargetCount < 0 || manifest.HistoryCount < 0 || filepath.Base(manifest.Snapshot) != manifest.Snapshot || manifest.Snapshot != "legacy-"+manifest.StateSHA256+"-"+manifest.SnapshotSHA256+".sqlite" {
			return d, invalid("invalid migration manifest")
		}
	}
	return d, nil
}

func (s *Store) saveRestoreBackup(data []byte) error {
	dir, err := s.backupDir()
	if err != nil {
		return err
	}
	id, err := newID()
	if err != nil {
		return err
	}
	name := "restore-" + time.Now().UTC().Format("20060102T150405.000000000") + "-" + id + ".json"
	path := filepath.Join(dir, name)
	if err = atomicFile(path, data); err != nil {
		return err
	}
	return atomicFile(path+".sha256", []byte(sha(data)+"  "+name+"\n"))
}

// Restore validates the complete document before touching disk. The caller
// must reject active jobs. Domain replacement, settings, daily maxima and
// migration-marker preservation happen in ONE transaction, after a durable
// automatic backup of the previous, consistently-read state.
func (s *Store) Restore(data []byte) error {
	d, err := validateBackup(data)
	if err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error {
		before, err := exportFrom(tx)
		if err != nil {
			return err
		}
		if err = s.saveRestoreBackup(before); err != nil {
			return err
		}
		for _, table := range []string{"fingerprint_samples", "attribution_results", "fingerprint_batches", "model_targets", "challenge_templates", "reference_banks", "legacy_history"} {
			if _, err = tx.Exec("DELETE FROM " + table); err != nil {
				return err
			}
		}
		for _, target := range d.Tables.Targets {
			// A public checksum proves transport integrity, not the provenance
			// of imported route/identity decisions or permission to spend again.
			target.LastStatus, target.LastCheckedAt, target.RetryAfter = "PENDING", "", ""
			target.AutoCheck = false
			if err = writeTarget(tx, target); err != nil {
				return err
			}
		}
		for _, template := range d.Tables.Templates {
			if err = writeTemplate(tx, template); err != nil {
				return err
			}
		}
		for _, bank := range d.Tables.Banks {
			bank.Trusted = false
			previous := unverifiedPayload(bank.ValidationMetrics, "imported_unverified_metrics")
			bank.ValidationMetrics = map[string]any{"identity_validated": false, "independent_validation": false, "review_status": "RESTORED_SOURCE_REVIEW_REQUIRED", "imported_unverified_metrics": previous}
			if err = writeBank(tx, bank.Bank); err != nil {
				return err
			}
		}
		for _, batch := range d.Tables.Batches {
			batch.Status, batch.RouteBindingStatus = "INCONCLUSIVE", "UNVERIFIED"
			batch.ErrorMessage = "RESTORED_EVIDENCE_REQUIRES_RECHECK"
			if err = writeBatch(tx, batch); err != nil {
				return err
			}
		}
		for _, sample := range d.Tables.Samples {
			previous := unverifiedPayload(sample.RouteEvidence, "imported_unverified_evidence")
			sample.RouteEvidence = map[string]any{"authenticated": false, "protocol_verified": false, "reason": "RESTORED_EVIDENCE_REQUIRES_RECHECK", "imported_unverified_evidence": previous}
			if err = writeSample(tx, sample); err != nil {
				return err
			}
		}
		for _, result := range d.Tables.Results {
			previous := unverifiedPayload(result.Metrics, "imported_unverified_metrics")
			result.IdentityStatus, result.RejectionReason = "INCONCLUSIVE", "RESTORED_EVIDENCE_REQUIRES_RECHECK"
			result.PredictedModel, result.PredictedFamily = "", ""
			result.CalibratedProbability = nil
			result.Metrics = map[string]any{"identity_validated": false, "bank_trusted": false, "protocol_verified": false,
				"restored_evidence": true, "candidate_method": "imported_descriptive_only", "imported_unverified_metrics": previous}
			if err = writeResult(tx, result); err != nil {
				return err
			}
		}
		for index, record := range d.Tables.Legacy {
			if _, err = tx.Exec("INSERT INTO legacy_history(id,model,time,verdict,data,ordinal) VALUES(?,?,?,?,?,?)", record.ID, record.Model, record.Time, record.Verdict, []byte(record.LegacyRecord.Data), index); err != nil {
				return err
			}
		}
		if err = writeSettings(tx, d.Tables.Settings); err != nil {
			return err
		}
		// No DELETE on daily_usage, including days absent from the backup.
		for _, usage := range d.Tables.Usage {
			if err = maxUsage(tx, usage); err != nil {
				return err
			}
		}
		for _, meta := range d.Tables.Meta {
			if _, err = tx.Exec("INSERT INTO store_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO NOTHING", meta.Key, meta.Value); err != nil {
				return err
			}
		}
		rows, err := tx.Query("PRAGMA foreign_key_check")
		if err != nil {
			return err
		}
		defer rows.Close()
		if rows.Next() {
			return invalid("restored foreign-key violation")
		}
		if err = rows.Err(); err != nil {
			return err
		}
		return nil
	})
}

func unverifiedPayload(value map[string]any, key string) any {
	if nested, ok := value[key]; ok {
		return nested
	}
	return value
}
