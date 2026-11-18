package modelstore

import (
	"database/sql"
	"fmt"
)

// Schema v1 is additive and lives in a transaction with its user_version bump.
// The six domain tables are normalized; settings, immutable daily accounting,
// quality-only history and migration markers are separate supporting tables.
const schemaV1 = `
CREATE TABLE IF NOT EXISTS model_targets (
 id TEXT PRIMARY KEY NOT NULL, provider_id TEXT NOT NULL DEFAULT '',
 model_id TEXT NOT NULL, claimed_model TEXT NOT NULL,
 expected_family TEXT NOT NULL DEFAULT '', expected_version TEXT NOT NULL DEFAULT '',
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
 auto_check INTEGER NOT NULL DEFAULT 0 CHECK(auto_check IN (0,1)),
 interval_minutes INTEGER NOT NULL DEFAULT 0 CHECK(interval_minutes>=0),
 request_budget INTEGER NOT NULL DEFAULT 0 CHECK(request_budget>=0),
 last_status TEXT NOT NULL DEFAULT 'PENDING', last_checked_at TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT '', retry_after TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS challenge_templates (
 id TEXT PRIMARY KEY NOT NULL, name TEXT NOT NULL, version INTEGER NOT NULL CHECK(version>=1),
 prompt TEXT NOT NULL, prompt_sha256 TEXT NOT NULL, parser_version TEXT NOT NULL,
 created_at TEXT NOT NULL, parameters_json TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS fingerprint_batches (
 id TEXT PRIMARY KEY NOT NULL, target_id TEXT REFERENCES model_targets(id) ON DELETE SET NULL,
 claimed_model TEXT NOT NULL, algorithm_version TEXT NOT NULL, challenge_version TEXT NOT NULL,
 bank_version TEXT NOT NULL, status TEXT NOT NULL, route_binding_status TEXT NOT NULL,
 started_at TEXT NOT NULL, completed_at TEXT NOT NULL, error_message TEXT NOT NULL,
 valid_samples INTEGER NOT NULL CHECK(valid_samples>=0)
);
CREATE TABLE IF NOT EXISTS fingerprint_samples (
 id TEXT PRIMARY KEY NOT NULL, target_id TEXT REFERENCES model_targets(id) ON DELETE SET NULL,
 batch_id TEXT NOT NULL REFERENCES fingerprint_batches(id) ON DELETE CASCADE,
 source_type TEXT NOT NULL, challenge_id TEXT NOT NULL, parser_version TEXT NOT NULL,
 raw_response TEXT NOT NULL, invalid_reason TEXT NOT NULL, response_model_field TEXT NOT NULL,
 response_sha256 TEXT NOT NULL, system_prompt_hash TEXT NOT NULL, request_id TEXT NOT NULL,
 collected_at TEXT NOT NULL, challenge_index INTEGER NOT NULL CHECK(challenge_index>=0),
 latency_ms INTEGER NOT NULL CHECK(latency_ms>=0), input_tokens INTEGER NOT NULL CHECK(input_tokens>=0),
 output_tokens INTEGER NOT NULL CHECK(output_tokens>=0), valid INTEGER NOT NULL CHECK(valid IN (0,1)),
 parsed_numbers_json TEXT NOT NULL, features_json TEXT NOT NULL,
 request_params_json TEXT NOT NULL, route_evidence_json TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS attribution_results (
 id TEXT PRIMARY KEY NOT NULL, batch_id TEXT NOT NULL UNIQUE REFERENCES fingerprint_batches(id) ON DELETE CASCADE,
 expected_model TEXT NOT NULL, predicted_model TEXT NOT NULL, predicted_family TEXT NOT NULL,
 identity_status TEXT NOT NULL, rejection_reason TEXT NOT NULL, bank_version TEXT NOT NULL,
 algorithm_version TEXT NOT NULL, created_at TEXT NOT NULL, top1_score REAL, top2_score REAL,
 score_margin REAL, calibrated_probability REAL CHECK(calibrated_probability IS NULL OR calibrated_probability BETWEEN 0 AND 1),
 candidates_json TEXT NOT NULL, metrics_json TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS reference_banks (
 id TEXT PRIMARY KEY NOT NULL, version TEXT NOT NULL, algorithm_version TEXT NOT NULL,
 challenge_version TEXT NOT NULL, sha256 TEXT NOT NULL, source_type TEXT NOT NULL,
 provenance TEXT NOT NULL, created_at TEXT NOT NULL,
 model_count INTEGER NOT NULL CHECK(model_count>=0), sample_count INTEGER NOT NULL CHECK(sample_count>=0),
 trusted INTEGER NOT NULL CHECK(trusted IN (0,1)), data BLOB NOT NULL, validation_metrics_json TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS store_settings (
 id INTEGER PRIMARY KEY CHECK(id=1), daily_requests INTEGER NOT NULL CHECK(daily_requests BETWEEN 0 AND 1000000),
 daily_tokens INTEGER NOT NULL CHECK(daily_tokens BETWEEN 0 AND 1000000000), concurrency INTEGER NOT NULL CHECK(concurrency BETWEEN 1 AND 8),
 timeout_seconds INTEGER NOT NULL CHECK(timeout_seconds BETWEEN 1 AND 120), retention_days INTEGER NOT NULL CHECK(retention_days BETWEEN 1 AND 3650)
);
CREATE TABLE IF NOT EXISTS daily_usage (
 day TEXT PRIMARY KEY NOT NULL, requests INTEGER NOT NULL CHECK(requests>=0),
 tokens INTEGER NOT NULL CHECK(tokens>=0)
);
CREATE TABLE IF NOT EXISTS legacy_history (
 id TEXT PRIMARY KEY NOT NULL, model TEXT NOT NULL, time TEXT NOT NULL,
 verdict TEXT NOT NULL, data BLOB NOT NULL, ordinal INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS store_meta (key TEXT PRIMARY KEY NOT NULL, value TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS batches_target_time ON fingerprint_batches(target_id,started_at DESC,id);
CREATE INDEX IF NOT EXISTS samples_batch_index ON fingerprint_samples(batch_id,challenge_index,id);
CREATE INDEX IF NOT EXISTS history_time ON legacy_history(time DESC,id);
`

func (s *Store) migrateSchema() error {
	return s.withTx(func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version > SchemaVersion {
			return invalid("unsupported schema %d", version)
		}
		if version == 0 {
			if _, err := tx.Exec(schemaV1); err != nil {
				return err
			}
			settings := DefaultSettings()
			if _, err := tx.Exec("INSERT OR IGNORE INTO store_settings(id,daily_requests,daily_tokens,concurrency,timeout_seconds,retention_days) VALUES(1,?,?,?,?,?)", settings.DailyRequests, settings.DailyTokens, settings.Concurrency, settings.TimeoutSeconds, settings.RetentionDays); err != nil {
				return err
			}
			if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion)); err != nil {
				return err
			}
		}
		for _, query := range []string{
			"SELECT " + targetColumns + " FROM model_targets LIMIT 0",
			"SELECT " + batchColumns + " FROM fingerprint_batches LIMIT 0",
			"SELECT " + sampleColumns + " FROM fingerprint_samples LIMIT 0",
			"SELECT " + resultColumns + " FROM attribution_results LIMIT 0",
			"SELECT " + templateColumns + " FROM challenge_templates LIMIT 0",
			"SELECT " + bankColumns + " FROM reference_banks LIMIT 0",
			"SELECT key,value FROM store_meta LIMIT 0",
			"SELECT id,model,time,verdict,data,ordinal FROM legacy_history LIMIT 0",
			"SELECT day,requests,tokens FROM daily_usage LIMIT 0",
		} {
			rows, err := tx.Query(query)
			if err != nil {
				return err
			}
			if err = rows.Close(); err != nil {
				return err
			}
		}
		_, err := settingsFrom(tx)
		return err
	})
}
