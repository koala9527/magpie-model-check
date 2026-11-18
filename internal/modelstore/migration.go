package modelstore

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const legacyMarker = "legacy_migration"

type legacyTarget struct {
	Model      string `json:"model"`
	Enabled    bool   `json:"enabled"`
	CheckedAt  string `json:"checked_at"`
	RetryAfter string `json:"retry_after"`
}

type legacyState struct {
	Targets      map[string]*legacyTarget  `json:"targets"`
	Results      []json.RawMessage         `json:"results"`
	ClearedUsage map[string]map[string]int `json:"cleared_usage"`
}

type migrationManifest struct {
	SourceSHA256   string `json:"source_sha256"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
	StateSHA256    string `json:"state_sha256"`
	Snapshot       string `json:"snapshot"`
	TargetCount    int    `json:"target_count"`
	HistoryCount   int    `json:"history_count"`
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Store) backupDir() (string, error) {
	dir := filepath.Join(s.dir, "backups")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func atomicFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".store-backup-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

// VACUUM INTO produces a consistent standalone SQLite snapshot, including
// committed WAL pages. Copying just check.db would lose live WAL transactions.
// The connection to the original is explicitly mode=ro; only its backup is
// queried for migration, so the JSON and backup describe the same snapshot.
func (s *Store) snapshotLegacy(path string) (migrationManifest, []byte, error) {
	var manifest migrationManifest
	sourceSHA, err := fileSHA(path)
	if err != nil {
		return manifest, nil, err
	}
	dir, err := s.backupDir()
	if err != nil {
		return manifest, nil, err
	}
	id, err := newID()
	if err != nil {
		return manifest, nil, err
	}
	temp := filepath.Join(dir, ".legacy-"+id+".sqlite")
	defer os.Remove(temp)
	source, err := sql.Open("sqlite", sqliteURI(path, true))
	if err != nil {
		return manifest, nil, err
	}
	source.SetMaxOpenConns(1)
	var exists int
	err = source.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='state'").Scan(&exists)
	if err == nil && exists != 1 {
		err = invalid("legacy database has no state table")
	}
	if err == nil {
		_, err = source.Exec("VACUUM INTO ?", temp)
	}
	closeErr := source.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return manifest, nil, err
	}
	if err = os.Chmod(temp, 0600); err != nil {
		return manifest, nil, err
	}
	// SQLite completed and synced the snapshot before returning. Sync the file
	// again before publishing its name and checksum alongside the migration.
	f, err := os.OpenFile(temp, os.O_RDWR, 0600)
	if err != nil {
		return manifest, nil, err
	}
	err = f.Sync()
	closeErr = f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return manifest, nil, err
	}
	snapshot, err := sql.Open("sqlite", sqliteURI(temp, true))
	if err != nil {
		return manifest, nil, err
	}
	var body []byte
	err = snapshot.QueryRow("SELECT body FROM state WHERE id=1").Scan(&body)
	if err == sql.ErrNoRows {
		err = nil
	}
	closeErr = snapshot.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return manifest, nil, err
	}
	backupSHA, err := fileSHA(temp)
	if err != nil {
		return manifest, nil, err
	}
	manifest = migrationManifest{SourceSHA256: sourceSHA, SnapshotSHA256: backupSHA, StateSHA256: sha(body)}
	manifest.Snapshot = "legacy-" + manifest.StateSHA256 + "-" + backupSHA + ".sqlite"
	destination := filepath.Join(dir, manifest.Snapshot)
	if _, err = os.Stat(destination); err == nil {
		existingSHA, hashErr := fileSHA(destination)
		if hashErr != nil {
			return manifest, nil, hashErr
		}
		if existingSHA != backupSHA {
			return manifest, nil, invalid("existing legacy backup hash mismatch")
		}
	} else if os.IsNotExist(err) {
		if err = os.Rename(temp, destination); err != nil {
			return manifest, nil, err
		}
	} else {
		return manifest, nil, err
	}
	if err = atomicFile(destination+".sha256", []byte(backupSHA+"  "+manifest.Snapshot+"\n")); err != nil {
		return manifest, nil, err
	}
	return manifest, body, nil
}

func addCount(a, b int) (int, error) {
	if a < 0 || b < 0 || b > int(^uint(0)>>1)-a {
		return 0, invalid("usage counter overflow/negative value")
	}
	return a + b, nil
}

func parseLegacy(body []byte) ([]Target, []LegacyRecord, map[string]Usage, map[string]bool, error) {
	var state legacyState
	if len(body) > 0 {
		if err := checkJSON(body, false); err != nil {
			return nil, nil, nil, nil, err
		}
		if bytes.TrimSpace(body)[0] != '{' {
			return nil, nil, nil, nil, invalid("legacy state must be an object")
		}
		if err := json.Unmarshal(body, &state); err != nil {
			return nil, nil, nil, nil, invalid("legacy state: %v", err)
		}
	}
	targets := make([]Target, 0, len(state.Targets))
	keys := make([]string, 0, len(state.Targets))
	for key := range state.Targets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	stamp := timestamp()
	for _, key := range keys {
		old := state.Targets[key]
		if old == nil {
			return nil, nil, nil, nil, invalid("null legacy target")
		}
		model := old.Model
		if model == "" {
			model = key
		}
		target := Target{ID: "legacy-" + sha([]byte(key))[:24], ModelID: model, ClaimedModel: model, Enabled: old.Enabled, AutoCheck: false, LastStatus: "PENDING", LastCheckedAt: legacyTime(old.CheckedAt), RetryAfter: legacyTime(old.RetryAfter), CreatedAt: stamp, UpdatedAt: stamp}
		if err := validateTarget(target); err != nil {
			return nil, nil, nil, nil, err
		}
		targets = append(targets, target)
	}
	history := make([]LegacyRecord, 0, len(state.Results))
	ids := map[string]bool{}
	usage := map[string]Usage{}
	unknownClearedTokens := map[string]bool{}
	for index, raw := range state.Results {
		if err := checkJSON(raw, true); err != nil {
			return nil, nil, nil, nil, err
		}
		var old struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Time    string `json:"time"`
			Verdict string `json:"verdict"`
			Tokens  int    `json:"tokens"`
		}
		if err := json.Unmarshal(raw, &old); err != nil {
			return nil, nil, nil, nil, invalid("legacy record: %v", err)
		}
		if err := validTimes(old.Time); err != nil {
			return nil, nil, nil, nil, err
		}
		id := old.ID
		if validID(id) != nil || ids[id] {
			id = fmt.Sprintf("legacy-record-%d-%s", index, sha(raw))
		}
		for ids[id] {
			id += "-duplicate"
		}
		ids[id] = true
		history = append(history, LegacyRecord{ID: id, Model: old.Model, Time: old.Time, Verdict: old.Verdict, Data: raw})
		if old.Tokens < 0 {
			return nil, nil, nil, nil, invalid("negative legacy tokens")
		}
		if old.Time != "" {
			at, _ := time.Parse(time.RFC3339Nano, old.Time)
			day := at.UTC().Format("2006-01-02")
			u := usage[day]
			u.Day = day
			var err error
			if u.Requests, err = addCount(u.Requests, 1); err != nil {
				return nil, nil, nil, nil, err
			}
			if u.Tokens, err = addCount(u.Tokens, old.Tokens); err != nil {
				return nil, nil, nil, nil, err
			}
			usage[day] = u
		}
	}
	for day, models := range state.ClearedUsage {
		if err := validDay(day); err != nil {
			return nil, nil, nil, nil, err
		}
		u := usage[day]
		u.Day = day
		for _, count := range models {
			var err error
			if u.Requests, err = addCount(u.Requests, count); err != nil {
				return nil, nil, nil, nil, err
			}
			if count > 0 {
				unknownClearedTokens[day] = true
			}
		}
		usage[day] = u
	}
	return targets, history, usage, unknownClearedTokens, nil
}

func legacyTime(v string) string {
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil && t.IsZero() {
		return ""
	}
	return v
}

func (s *Store) migrateLegacy(path string) error {
	if path == "" {
		return nil
	}
	var marker string
	if err := s.db.QueryRow("SELECT value FROM store_meta WHERE key=?", legacyMarker).Scan(&marker); err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	currentInfo, err := os.Stat(s.path)
	if err != nil {
		return err
	}
	if os.SameFile(info, currentInfo) {
		return invalid("legacy and new databases must be different files")
	}
	manifest, body, err := s.snapshotLegacy(abs)
	if err != nil {
		return err
	}
	targets, history, usage, unknown, err := parseLegacy(body)
	if err != nil {
		return err
	}
	manifest.TargetCount, manifest.HistoryCount = len(targets), len(history)
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err = atomicFile(filepath.Join(s.dir, "backups", manifest.Snapshot)+".json", manifestJSON); err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error {
		// Another process may have completed migration while we made the snapshot.
		if err := tx.QueryRow("SELECT value FROM store_meta WHERE key=?", legacyMarker).Scan(&marker); err == nil {
			return nil
		} else if err != sql.ErrNoRows {
			return err
		}
		for _, target := range targets {
			var exists int
			if err := tx.QueryRow("SELECT count(*) FROM model_targets WHERE id=?", target.ID).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				if err := writeTarget(tx, target); err != nil {
					return err
				}
			}
		}
		for index, record := range history {
			var existing []byte
			err := tx.QueryRow("SELECT data FROM legacy_history WHERE id=?", record.ID).Scan(&existing)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if err == nil && !bytes.Equal(existing, record.Data) {
				record.ID = fmt.Sprintf("legacy-record-%d-%s", index, sha(record.Data))
			}
			if _, err = tx.Exec("INSERT INTO legacy_history(id,model,time,verdict,data,ordinal) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING", record.ID, record.Model, record.Time, record.Verdict, []byte(record.Data), index); err != nil {
				return err
			}
		}
		settings, err := settingsFrom(tx)
		if err != nil {
			return err
		}
		for day, imported := range usage {
			// Deleted legacy rows have no token totals. Burn at least that day's
			// configured token cap rather than silently inventing a zero charge.
			if unknown[day] && imported.Tokens < settings.DailyTokens {
				imported.Tokens = settings.DailyTokens
			}
			current, err := usageFrom(tx, day)
			if err != nil {
				return err
			}
			if imported.Requests, err = addCount(imported.Requests, current.Requests); err != nil {
				return err
			}
			if imported.Tokens, err = addCount(imported.Tokens, current.Tokens); err != nil {
				return err
			}
			if err = maxUsage(tx, imported); err != nil {
				return err
			}
		}
		_, err = tx.Exec("INSERT INTO store_meta(key,value) VALUES(?,?)", legacyMarker, string(manifestJSON))
		return err
	})
}
