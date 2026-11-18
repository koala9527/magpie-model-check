package modelstore

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const DatabaseName = "model-check.db"

type Store struct {
	db      *sql.DB
	dir     string
	path    string
	writeMu sync.Mutex
}

// Open creates/opens the new database. An absent legacyDB is a fresh install;
// an existing legacyDB is only read, never rewritten or removed.
func Open(dir string, legacyDB string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, invalid("empty storage directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(abs, DatabaseName)
	db, err := sql.Open("sqlite", sqliteURI(path, false))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, dir: abs, path: path}
	fail := func(err error) (*Store, error) { _ = db.Close(); return nil, err }
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	if version > SchemaVersion {
		return fail(invalid("unsupported schema %d (supported %d)", version, SchemaVersion))
	}
	if _, err = db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fail(err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		return fail(err)
	}
	if err = s.migrateSchema(); err != nil {
		return fail(err)
	}
	if err = s.migrateLegacy(legacyDB); err != nil {
		return fail(err)
	}
	return s, nil
}

func sqliteURI(path string, readOnly bool) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	q := url.Values{"_pragma": {"busy_timeout(10000)"}}
	if readOnly {
		q.Set("mode", "ro")
	} else {
		q["_pragma"] = append(q["_pragma"], "foreign_keys(ON)", "synchronous(FULL)")
		q.Set("_txlock", "immediate")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Store) Close() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.db.Close()
}

// BEGIN IMMEDIATE serializes the settings read and ledger mutation across
// processes as well as goroutines. WAL readers continue to see committed data.
func (s *Store) withTx(fn func(*sql.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

type scanner interface{ Scan(...any) error }
type querier interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func missing(err error, id string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return err
}

func timestamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func deleteOne(tx *sql.Tx, table, id string) error {
	r, err := tx.Exec("DELETE FROM "+table+" WHERE id=?", id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n == 0 {
		return missing(sql.ErrNoRows, id)
	}
	return err
}
