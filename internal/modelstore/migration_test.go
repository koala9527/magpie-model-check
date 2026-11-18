package modelstore

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const legacyFixture = `{
  "config":{"gateway_url":"http://localhost:9999","gateway_key":"DO-NOT-EXPORT-THIS-KEY","daily_limit":120},
  "alias":"DO-NOT-EXPORT-THIS-ALIAS",
  "targets":{"provider/model":{"model":"provider/model","enabled":true,"state":"passed","checked_at":"2026-11-17T08:00:00Z","retry_after":"2026-11-17T09:00:00Z"}},
  "results":[
    {"id":"record-1", "model":"provider/model", "verdict":"passed", "time":"2026-11-17T08:00:00Z", "answer":"original answer", "tokens":23, "review":"accepted"},
    {"id":"record-2", "model":"provider/model", "verdict":"unavailable", "time":"2026-11-17T08:01:00Z", "error":"original error", "tokens":19}
  ]
}`

func createLegacy(t *testing.T, body []byte, wal bool) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "check.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if wal {
		if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec("CREATE TABLE state(id INTEGER PRIMARY KEY CHECK(id=1), body BLOB NOT NULL); INSERT INTO state(id,body) VALUES(1,?)", body); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return path, db
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func matches(t *testing.T, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestLegacyMigrationReadOnlyIdempotentAndSnapshotSHA(t *testing.T) {
	for _, wal := range []bool{false, true} {
		t.Run(fmt.Sprintf("live_WAL_%t", wal), func(t *testing.T) {
			legacyPath, legacyDB := createLegacy(t, []byte(legacyFixture), wal)
			if !wal {
				_ = legacyDB.Close()
			}
			original := readFile(t, legacyPath)
			dir := t.TempDir()
			for range 3 {
				s, err := Open(dir, legacyPath)
				if err != nil {
					t.Fatal(err)
				}
				targets, err := s.Targets()
				if err != nil || len(targets) != 1 || targets[0].ModelID != "provider/model" || !targets[0].Enabled || targets[0].AutoCheck || targets[0].LastStatus == "MATCHED" {
					t.Fatalf("migration target: %#v %v", targets, err)
				}
				history, err := s.Legacy()
				if err != nil || len(history) != 2 {
					t.Fatalf("legacy history: %#v %v", history, err)
				}
				var state struct {
					Results []json.RawMessage `json:"results"`
				}
				if err = json.Unmarshal([]byte(legacyFixture), &state); err != nil {
					t.Fatal(err)
				}
				for _, record := range history {
					index := 0
					if record.ID == "record-2" {
						index = 1
					}
					if !bytes.Equal(record.Data, state.Results[index]) {
						t.Fatalf("original record bytes changed: %s", record.Data)
					}
					if record.ID == "record-1" && record.Verdict != "passed" {
						t.Fatalf("quality verdict reinterpreted: %s", record.Verdict)
					}
				}
				u, err := s.Usage("2026-11-17")
				if err != nil {
					t.Fatal(err)
				}
				assertJSON(t, u, Usage{Day: "2026-11-17", Requests: 2, Tokens: 42})
				exported, err := s.Export()
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(exported, []byte("DO-NOT-EXPORT")) || bytes.Contains(exported, []byte("gateway_key")) || bytes.Contains(exported, []byte(`"config"`)) {
					t.Fatal("secret config exported")
				}
				batches, err := s.Batches("", 0)
				if err != nil || len(batches) != 0 {
					t.Fatal("legacy quality history became identity evidence", batches, err)
				}
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(original, readFile(t, legacyPath)) {
				t.Fatal("migration modified original SQLite bytes")
			}
			snapshots := matches(t, filepath.Join(dir, "backups", "legacy-*.sqlite"))
			if len(snapshots) != 1 {
				t.Fatalf("snapshots=%v, want one despite three opens", snapshots)
			}
			backupBytes := readFile(t, snapshots[0])
			shaFile := strings.Fields(string(readFile(t, snapshots[0]+".sha256")))
			if len(shaFile) == 0 || shaFile[0] != digest(backupBytes) {
				t.Fatal("snapshot SHA mismatch")
			}
			var manifest struct {
				SourceSHA256   string `json:"source_sha256"`
				SnapshotSHA256 string `json:"snapshot_sha256"`
				StateSHA256    string `json:"state_sha256"`
				TargetCount    int    `json:"target_count"`
				HistoryCount   int    `json:"history_count"`
			}
			if err := json.Unmarshal(readFile(t, snapshots[0]+".json"), &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.SourceSHA256 != digest(original) || manifest.SnapshotSHA256 != digest(backupBytes) || manifest.StateSHA256 != digest([]byte(legacyFixture)) || manifest.TargetCount != 1 || manifest.HistoryCount != 2 {
				t.Fatalf("invalid migration manifest: %+v", manifest)
			}
			snapshotDB, err := sql.Open("sqlite", snapshots[0])
			if err != nil {
				t.Fatal(err)
			}
			defer snapshotDB.Close()
			var restoredBody []byte
			if err = snapshotDB.QueryRow("SELECT body FROM state WHERE id=1").Scan(&restoredBody); err != nil || !bytes.Equal(restoredBody, []byte(legacyFixture)) {
				t.Fatal("original SQLite snapshot is not recoverable", err)
			}
			t.Logf("targets=1 history=2 opens=3 snapshots=1 source_unchanged=true source_sha256=%s snapshot_sha256=%s state_sha256=%s", manifest.SourceSHA256, manifest.SnapshotSHA256, manifest.StateSHA256)
		})
	}
}

func TestLegacyMigrationTransactionFailureAndRetry(t *testing.T) {
	legacyPath, _ := createLegacy(t, []byte(legacyFixture), false)
	dir := t.TempDir()
	s, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER interrupt_legacy BEFORE INSERT ON legacy_history WHEN NEW.id='record-2' BEGIN SELECT RAISE(ABORT,'migration interruption'); END`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if failed, err := Open(dir, legacyPath); err == nil {
		_ = failed.Close()
		t.Fatal("interrupted migration succeeded")
	}
	s, err = Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	targets, _ := s.Targets()
	history, _ := s.Legacy()
	u, _ := s.Usage("2026-11-17")
	if len(targets) != 0 || len(history) != 0 || u.Requests != 0 || u.Tokens != 0 {
		t.Fatalf("partial migration committed: %v %v %+v", targets, history, u)
	}
	if _, err = s.db.Exec("DROP TRIGGER interrupt_legacy"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	for range 2 {
		s, err = Open(dir, legacyPath)
		if err != nil {
			t.Fatal(err)
		}
		history, _ = s.Legacy()
		if len(history) != 2 {
			t.Fatalf("retry lost/duplicated history: %d", len(history))
		}
		_ = s.Close()
	}
}

func TestLegacyClearedUsageIsNotRefundedAndIDsDoNotLoseHistory(t *testing.T) {
	body := []byte(`{"targets":{},"results":[{"id":"duplicate","model":"m","time":"2026-11-17T08:00:00Z","verdict":"passed","tokens":5},{"id":"duplicate","model":"m","time":"2026-11-17T08:01:00Z","verdict":"unavailable","tokens":0},{"model":"m","time":"2026-11-17T08:02:00Z","verdict":"running","tokens":0}],"cleared_usage":{"2026-11-17":{"m":58}}}`)
	legacyPath, _ := createLegacy(t, body, false)
	s, err := Open(t.TempDir(), legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	history, err := s.Legacy()
	if err != nil || len(history) != 3 {
		t.Fatalf("duplicate/missing legacy IDs lost history: %v %v", history, err)
	}
	u, err := s.Usage("2026-11-17")
	if err != nil || u.Requests != 61 || u.Tokens < DefaultSettings().DailyTokens {
		t.Fatalf("cleared usage refunded: %+v %v", u, err)
	}
	if err = s.Reserve("2026-11-17", 1, 1); err == nil {
		t.Fatal("clearing old history opened a fresh budget")
	}
}

func TestCorruptLegacyIsRejectedWithoutPartialImport(t *testing.T) {
	legacyPath, _ := createLegacy(t, []byte(`{"targets":{"m":`), false)
	dir := t.TempDir()
	if s, err := Open(dir, legacyPath); err == nil {
		_ = s.Close()
		t.Fatal("corrupt legacy JSON accepted")
	}
	s, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	targets, _ := s.Targets()
	history, _ := s.Legacy()
	if len(targets) != 0 || len(history) != 0 {
		t.Fatal("partial corrupt legacy imported")
	}
}

func TestPruneKeepsLegacyHistoryAndOriginalSnapshotsForever(t *testing.T) {
	oldBody := []byte(strings.ReplaceAll(legacyFixture, "2026-11-17", "2000-01-01"))
	legacyPath, _ := createLegacy(t, oldBody, false)
	dir := t.TempDir()
	s, err := Open(dir, legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	history, err := s.Legacy()
	if err != nil || len(history) != 2 {
		t.Fatal(history, err)
	}
	snapshots := matches(t, filepath.Join(dir, "backups", "legacy-*.sqlite"))
	if len(snapshots) != 1 {
		t.Fatal("legacy recovery snapshot missing")
	}
	beforeSHA := digest(readFile(t, snapshots[0]))
	_, batch, sample, result, _, _ := seedRecords(t, s)
	old := time.Now().UTC().AddDate(0, 0, -90).Format(time.RFC3339Nano)
	batch.StartedAt, batch.CompletedAt, sample.CollectedAt, result.CreatedAt = old, old, old, old
	for _, err := range []error{s.PutBatch(batch), s.PutSample(sample), s.PutResult(result), s.Reserve("2026-11-17", 60, 300000), s.Prune(30)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	retained, err := s.Legacy()
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, retained, history)
	if afterSHA := digest(readFile(t, snapshots[0])); afterSHA != beforeSHA {
		t.Fatal("prune modified/deleted original recovery snapshot")
	}
	if _, err = s.Batch(batch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("new expired batch not pruned", err)
	}
	u, err := s.Usage("2000-01-01")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, u, Usage{Day: "2000-01-01", Requests: 2, Tokens: 42})
	u, err = s.Usage("2026-11-17")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, u, Usage{Day: "2026-11-17", Requests: 60, Tokens: 300000})
	t.Log("30-day prune: new expired history removed; year-2000 legacy records and original SQLite snapshot preserved; all daily budgets retained")
}

func TestLegacyExportRestorePreservesBytesAndMigrationMarker(t *testing.T) {
	legacyPath, _ := createLegacy(t, []byte(legacyFixture), true)
	source, err := Open(t.TempDir(), legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	originalHistory, err := source.Legacy()
	if err != nil {
		t.Fatal(err)
	}
	backup := exportTest(t, source)
	dir := t.TempDir()
	dest, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = dest.Restore(backup); err != nil {
		t.Fatal(err)
	}
	if err = dest.Close(); err != nil {
		t.Fatal(err)
	}
	// A migrated backup restores the completion marker, so restart with the
	// legacy path must not duplicate history, targets or historical accounting.
	dest, err = Open(dir, legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	history, err := dest.Legacy()
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, history, originalHistory)
	for i := range history {
		if !bytes.Equal(history[i].Data, originalHistory[i].Data) {
			t.Fatal("legacy export/restore compacted original record bytes")
		}
	}
	u, err := dest.Usage("2026-11-17")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, u, Usage{Day: "2026-11-17", Requests: 2, Tokens: 42})
	targets, err := dest.Targets()
	if err != nil || len(targets) != 1 {
		t.Fatal("restored legacy targets duplicated", targets, err)
	}
	if bytes.Contains(exportTest(t, dest), []byte("DO-NOT-EXPORT")) {
		t.Fatal("restored legacy secrets entered export")
	}
}

func TestSchemaMigrationDDLAndVersionRollBackTogether(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DatabaseName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE store_settings(id INTEGER PRIMARY KEY, daily_requests INTEGER NOT NULL, daily_tokens INTEGER NOT NULL, concurrency INTEGER NOT NULL, timeout_seconds INTEGER NOT NULL, retention_days INTEGER NOT NULL);
CREATE TRIGGER interrupt_defaults BEFORE INSERT ON store_settings BEGIN SELECT RAISE(ABORT,'schema interruption'); END`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if s, err := Open(dir, ""); err == nil {
		_ = s.Close()
		t.Fatal("interrupted schema initialization succeeded")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var version, count int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 0 {
		t.Fatalf("failed schema version=%d: %v", version, err)
	}
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='model_targets'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial domain DDL committed: %d %v", count, err)
	}
	if _, err = db.Exec("DROP TRIGGER interrupt_defaults"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("schema retry failed: version=%d %v", version, err)
	}
	settings, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, settings, DefaultSettings())
	t.Log("interrupted schema: user_version=0, no partial domain tables; retry: user_version=1 with defaults")
}

func TestUncommittedProcessExitIsRolledBack(t *testing.T) {
	if dir := os.Getenv("MODELSTORE_CRASH_PATH"); dir != "" {
		s, err := Open(dir, "")
		if err != nil {
			panic(err)
		}
		tx, err := s.db.Begin()
		if err != nil {
			panic(err)
		}
		if _, err = tx.Exec("INSERT INTO model_targets(id,model_id,claimed_model) VALUES('uncommitted','m','m'); INSERT INTO daily_usage(day,requests,tokens) VALUES('2026-11-18',1,100)"); err != nil {
			panic(err)
		}
		fmt.Println("exiting with an uncommitted target and reservation")
		os.Exit(23)
	}
	dir := t.TempDir()
	s, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Reserve("2026-11-17", 5, 500); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestUncommittedProcessExitIsRolledBack$")
	cmd.Env = append(os.Environ(), "MODELSTORE_CRASH_PATH="+dir)
	out, err := cmd.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 23 {
		t.Fatalf("crash helper: %s %v", out, err)
	}
	s, err = Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	targets, _ := s.Targets()
	u, _ := s.Usage("2026-11-18")
	prior, _ := s.Usage("2026-11-17")
	if len(targets) != 0 || u.Requests != 0 || u.Tokens != 0 || prior.Requests != 5 || prior.Tokens != 500 {
		t.Fatalf("process interruption not atomic: %v %+v %+v", targets, u, prior)
	}
	t.Logf("helper exit=23; committed ledger retained; uncommitted target/reservation rolled back; output=%q", out)
}
