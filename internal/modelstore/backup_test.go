package modelstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func exportTest(t *testing.T, s *Store) []byte {
	t.Helper()
	b, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Recomputing the digest lets these tests reach semantic validation, not just
// detect a stale checksum. This is test-only, never a reference-bank generator.
func mutateExport(t *testing.T, original []byte, fn func(map[string]any), rehash bool) []byte {
	t.Helper()
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(original))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	fn(document)
	if rehash {
		delete(document, "hash")
		document["hash"] = digest(mustJSON(t, document))
	}
	return mustJSON(t, document)
}

func TestExportRestorePreservesRawBytesAndUsageHighWater(t *testing.T) {
	source := openTestStore(t)
	target, _, _, _, _, bank := seedRecords(t, source)
	if err := source.Reserve("2026-11-17", 4, 9000); err != nil {
		t.Fatal(err)
	}
	backup := exportTest(t, source)
	if !bytes.Equal(backup, exportTest(t, source)) {
		t.Fatal("export/hash is not deterministic")
	}
	var envelope struct {
		Format string                     `json:"format"`
		Schema int                        `json:"schema"`
		Tables map[string]json.RawMessage `json:"tables"`
		Hash   string                     `json:"hash"`
	}
	if err := json.Unmarshal(backup, &envelope); err != nil || envelope.Format == "" || envelope.Schema != SchemaVersion || len(envelope.Hash) != 64 || len(envelope.Tables) < 9 {
		t.Fatalf("missing format/schema/tables/hash: %+v %v", envelope, err)
	}
	dir := t.TempDir()
	dest, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	if _, err = dest.PutTarget(Target{ID: "before-restore", ModelID: "original-local-model"}); err != nil {
		t.Fatal(err)
	}
	if err = dest.Reserve("2026-11-17", 7, 5000); err != nil {
		t.Fatal(err)
	}
	if err = dest.Reserve("2026-11-18", 3, 700); err != nil {
		t.Fatal(err)
	}
	before := exportTest(t, dest)
	if err = dest.Restore(backup); err != nil {
		t.Fatal(err)
	}
	got, err := dest.Target(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	expectedTarget := target
	expectedTarget.LastStatus, expectedTarget.LastCheckedAt, expectedTarget.RetryAfter = "PENDING", "", ""
	expectedTarget.AutoCheck = false
	assertJSON(t, got, expectedTarget)
	if _, err = dest.Target("before-restore"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore did not replace target table: %v", err)
	}
	u, err := dest.Usage("2026-11-17")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, u, Usage{Day: "2026-11-17", Requests: 7, Tokens: 9000})
	u, err = dest.Usage("2026-11-18")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, u, Usage{Day: "2026-11-18", Requests: 3, Tokens: 700})
	gotBank, err := dest.Bank(bank.ID)
	if err != nil || !bytes.Equal(gotBank.Data, bank.Data) || gotBank.SHA256 != digest(bank.Data) {
		t.Fatalf("export/restore changed original bank bytes/hash: %v", err)
	}
	backups := matches(t, filepath.Join(dir, "backups", "restore-*.json"))
	if len(backups) != 1 || !bytes.Equal(readFile(t, backups[0]), before) {
		t.Fatalf("pre-restore backup missing/wrong: %v", backups)
	}
	sha := strings.Fields(string(readFile(t, backups[0]+".sha256")))
	if len(sha) == 0 || sha[0] != digest(before) {
		t.Fatal("pre-restore backup SHA mismatch")
	}
	// Exercise rollback on a different store; leave the restored store changed.
	rollback := openTestStore(t)
	if err = rollback.Restore(readFile(t, backups[0])); err != nil {
		t.Fatal(err)
	}
	recovered, err := rollback.Target("before-restore")
	if err != nil || recovered.ModelID != "original-local-model" {
		t.Fatal("pre-restore backup does not recover prior target configuration", err)
	}
	for _, day := range []string{"2026-11-17", "2026-11-18"} {
		prior, _ := dest.Usage(day)
		restored, _ := rollback.Usage(day)
		if restored.Requests > prior.Requests || restored.Tokens > prior.Tokens {
			t.Fatal("rollback added usage")
		}
	}
	if err = dest.Restore(backup); err != nil {
		t.Fatal(err)
	}
	u, _ = dest.Usage("2026-11-17")
	assertJSON(t, u, Usage{Day: "2026-11-17", Requests: 7, Tokens: 9000})
	t.Log("restore replaced domain rows; requests/tokens independently max-merged; pre-restore backup recovered on a separate store; raw bank SHA retained")
}

func TestRestoreRejectsCorruptionBeforeBackupOrChanges(t *testing.T) {
	s := openTestStore(t)
	seedRecords(t, s)
	if err := s.Reserve("2026-11-17", 1, 1000); err != nil {
		t.Fatal(err)
	}
	original := exportTest(t, s)
	cases := map[string][]byte{
		"truncated":               original[:len(original)-1],
		"duplicate_top_level_key": []byte(strings.Replace(string(original), `"format":`, `"format":"duplicate","format":`, 1)),
		"unknown_field":           mutateExport(t, original, func(m map[string]any) { m["config"] = map[string]any{"gateway_key": "secret"} }, true),
		"checksum": mutateExport(t, original, func(m map[string]any) {
			m["tables"].(map[string]any)["model_targets"].([]any)[0].(map[string]any)["model_id"] = "changed"
		}, false),
		"future_schema": mutateExport(t, original, func(m map[string]any) { m["schema"] = SchemaVersion + 1 }, true),
		"missing_table": mutateExport(t, original, func(m map[string]any) { delete(m["tables"].(map[string]any), "daily_usage") }, true),
		"null_table":    mutateExport(t, original, func(m map[string]any) { m["tables"].(map[string]any)["model_targets"] = nil }, true),
		"duplicate_target": mutateExport(t, original, func(m map[string]any) {
			tables := m["tables"].(map[string]any)
			rows := tables["model_targets"].([]any)
			tables["model_targets"] = append(rows, rows[0])
		}, true),
		"orphan_sample": mutateExport(t, original, func(m map[string]any) {
			m["tables"].(map[string]any)["fingerprint_samples"].([]any)[0].(map[string]any)["batch_id"] = "missing"
		}, true),
		"bad_bank_sha": mutateExport(t, original, func(m map[string]any) {
			m["tables"].(map[string]any)["reference_banks"].([]any)[0].(map[string]any)["sha256"] = strings.Repeat("0", 64)
		}, true),
		"negative_usage": mutateExport(t, original, func(m map[string]any) {
			m["tables"].(map[string]any)["daily_usage"] = []any{map[string]any{"day": "2026-11-17", "requests": -1, "tokens": 0}}
		}, true),
		"unknown_row_field": mutateExport(t, original, func(m map[string]any) {
			m["tables"].(map[string]any)["model_targets"].([]any)[0].(map[string]any)["gateway_key"] = "secret"
		}, true),
		"duplicate_nested_key": []byte(strings.Replace(string(original), `"requests":`, `"requests":0,"requests":`, 1)),
		"trailing_document":    append(append([]byte{}, original...), []byte(` {}`)...),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(data, original) {
				t.Fatal("corruption fixture did not modify any bytes")
			}
			if err := s.Restore(data); err == nil {
				t.Fatal("malformed/tampered export accepted")
			}
			if !bytes.Equal(original, exportTest(t, s)) {
				t.Fatal("failed validation changed database")
			}
			if backups := matches(t, filepath.Join(s.dir, "backups", "restore-*.json")); len(backups) != 0 {
				t.Fatalf("invalid input created a pre-restore backup: %v", backups)
			}
		})
	}
}

func TestRestoreTransactionAbortPreservesOriginalAndBudget(t *testing.T) {
	source := openTestStore(t)
	seedRecords(t, source)
	backup := exportTest(t, source)
	dest := openTestStore(t)
	if _, err := dest.PutTarget(Target{ID: "original", ModelID: "original"}); err != nil {
		t.Fatal(err)
	}
	if err := dest.Reserve("2026-11-17", 6, 6000); err != nil {
		t.Fatal(err)
	}
	before := exportTest(t, dest)
	if _, err := dest.db.Exec(`CREATE TRIGGER interrupt_restore BEFORE INSERT ON fingerprint_samples BEGIN SELECT RAISE(ABORT,'restore interruption'); END`); err != nil {
		t.Fatal(err)
	}
	if err := dest.Restore(backup); err == nil {
		t.Fatal("interrupted restore succeeded")
	}
	if !bytes.Equal(before, exportTest(t, dest)) {
		t.Fatal("partial restore committed/decreased budget")
	}
	backups := matches(t, filepath.Join(dest.dir, "backups", "restore-*.json"))
	if len(backups) != 1 || !bytes.Equal(before, readFile(t, backups[0])) {
		t.Fatal("interrupted restore lost recoverable pre-restore backup")
	}
}

func TestRestoringOldExportCannotResetExhaustedBudget(t *testing.T) {
	s := openTestStore(t)
	target, _, _, _, _, bank := seedRecords(t, s)
	emptyUsageBackup := exportTest(t, s)
	if err := s.Reserve("2026-11-17", 60, 300000); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTarget(target.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBank(bank.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(1); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(emptyUsageBackup); err != nil {
		t.Fatal(err)
	}
	if err := s.Reserve("2026-11-17", 1, 1); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatal("restoring an earlier backup refunded spent budget", err)
	}
}
