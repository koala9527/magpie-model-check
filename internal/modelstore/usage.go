package modelstore

import (
	"database/sql"
	"time"
)

func settingsFrom(q querier) (s Settings, err error) {
	err = q.QueryRow("SELECT daily_requests,daily_tokens,concurrency,timeout_seconds,retention_days FROM store_settings WHERE id=1").Scan(&s.DailyRequests, &s.DailyTokens, &s.Concurrency, &s.TimeoutSeconds, &s.RetentionDays)
	if err == nil {
		err = validateSettings(s)
	}
	return
}

func (s *Store) Settings() (Settings, error) { return settingsFrom(s.db) }

func writeSettings(tx *sql.Tx, s Settings) error {
	_, err := tx.Exec("UPDATE store_settings SET daily_requests=?,daily_tokens=?,concurrency=?,timeout_seconds=?,retention_days=? WHERE id=1", s.DailyRequests, s.DailyTokens, s.Concurrency, s.TimeoutSeconds, s.RetentionDays)
	return err
}

func (s *Store) PutSettings(settings Settings) error {
	if err := validateSettings(settings); err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error { return writeSettings(tx, settings) })
}

func usageFrom(q querier, day string) (u Usage, err error) {
	u.Day = day
	err = q.QueryRow("SELECT requests,tokens FROM daily_usage WHERE day=?", day).Scan(&u.Requests, &u.Tokens)
	if err == sql.ErrNoRows {
		err = nil
	}
	return
}

func (s *Store) Usage(day string) (Usage, error) {
	if err := validDay(day); err != nil {
		return Usage{}, err
	}
	return usageFrom(s.db, day)
}

func maxUsage(tx *sql.Tx, u Usage) error {
	_, err := tx.Exec("INSERT INTO daily_usage(day,requests,tokens) VALUES(?,?,?) ON CONFLICT(day) DO UPDATE SET requests=MAX(daily_usage.requests,excluded.requests),tokens=MAX(daily_usage.tokens,excluded.tokens)", u.Day, u.Requests, u.Tokens)
	return err
}

// Reserve is conservative, final accounting, not a refundable ticket. It reads
// current settings in the same transaction as the charge and never overflows.
func (s *Store) Reserve(day string, requests, tokens int) error {
	if err := validDay(day); err != nil {
		return err
	}
	if requests < 0 || tokens < 0 {
		return invalid("negative reservation")
	}
	return s.withTx(func(tx *sql.Tx) error {
		settings, err := settingsFrom(tx)
		if err != nil {
			return err
		}
		u, err := usageFrom(tx, day)
		if err != nil {
			return err
		}
		if u.Requests > settings.DailyRequests || u.Tokens > settings.DailyTokens || requests > settings.DailyRequests-u.Requests || tokens > settings.DailyTokens-u.Tokens {
			return ErrBudgetExceeded
		}
		u.Requests += requests
		u.Tokens += tokens
		return maxUsage(tx, u)
	})
}

// ChargeExtra accounts for upstream tokens beyond the preflight reservation.
// It deliberately bypasses limits: debt survives restart/restore and prevents
// subsequent reservations, rather than silently dropping actual usage.
func (s *Store) ChargeExtra(day string, tokens int) error {
	if err := validDay(day); err != nil {
		return err
	}
	if tokens < 0 {
		return invalid("negative extra charge")
	}
	return s.withTx(func(tx *sql.Tx) error {
		u, err := usageFrom(tx, day)
		if err != nil {
			return err
		}
		if tokens > int(^uint(0)>>1)-u.Tokens {
			return invalid("extra charge overflows ledger")
		}
		u.Tokens += tokens
		return maxUsage(tx, u)
	})
}

// Prune touches only expired, non-active detection history. It never deletes
// settings, targets, challenges, reference banks, legacy quality history,
// usage or recovery snapshots. Legacy retention is independent of the new TTL.
func (s *Store) Prune(days int) error {
	if days <= 0 {
		return invalid("retention days must be positive")
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339Nano)
	return s.withTx(func(tx *sql.Tx) error {
		const terminal = "UPPER(status) NOT IN ('PENDING','RUNNING','CHECKING')"
		queries := []string{
			"DELETE FROM fingerprint_samples WHERE julianday(collected_at)<julianday(?) AND batch_id IN (SELECT id FROM fingerprint_batches WHERE " + terminal + ")",
			"DELETE FROM attribution_results WHERE julianday(created_at)<julianday(?) AND batch_id IN (SELECT id FROM fingerprint_batches WHERE " + terminal + ")",
			"DELETE FROM fingerprint_batches WHERE " + terminal + " AND julianday(COALESCE(NULLIF(completed_at,''),started_at))<julianday(?)",
		}
		for _, query := range queries {
			if _, err := tx.Exec(query, cutoff); err != nil {
				return err
			}
		}
		return nil
	})
}
