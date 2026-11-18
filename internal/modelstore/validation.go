package modelstore

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidData, fmt.Sprintf(format, args...))
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func sha(b []byte) string {
	v := sha256.Sum256(b)
	return hex.EncodeToString(v[:])
}

func validSHA(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}

func validID(s string) error {
	if strings.TrimSpace(s) == "" || len(s) > 512 || strings.ContainsAny(s, "\r\n\x00") {
		return invalid("invalid ID")
	}
	return nil
}

func validDay(s string) error {
	d, err := time.Parse("2006-01-02", s)
	if err != nil || d.Format("2006-01-02") != s {
		return invalid("day must be YYYY-MM-DD")
	}
	return nil
}

func validTimes(values ...string) error {
	for _, v := range values {
		if v != "" {
			if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
				return invalid("invalid RFC3339 timestamp")
			}
		}
	}
	return nil
}

func validateTarget(t Target) error {
	if err := validID(t.ID); err != nil {
		return err
	}
	if err := validID(t.ModelID); err != nil {
		return err
	}
	if t.IntervalMinutes < 0 || t.RequestBudget < 0 {
		return invalid("negative target interval/budget")
	}
	return validTimes(t.LastCheckedAt, t.CreatedAt, t.UpdatedAt, t.RetryAfter)
}

func validateSettings(s Settings) error {
	if s.DailyRequests < 0 || s.DailyRequests > 1000000 ||
		s.DailyTokens < 0 || s.DailyTokens > 1000000000 ||
		s.Concurrency < 1 || s.Concurrency > 8 ||
		s.TimeoutSeconds < 1 || s.TimeoutSeconds > 120 ||
		s.RetentionDays < 1 || s.RetentionDays > 3650 {
		return invalid("invalid settings limits")
	}
	return nil
}

func validateBatch(b Batch) error {
	if err := validID(b.ID); err != nil {
		return err
	}
	if b.TargetID != "" {
		if err := validID(b.TargetID); err != nil {
			return err
		}
	}
	if b.ValidSamples < 0 {
		return invalid("negative valid sample count")
	}
	return validTimes(b.StartedAt, b.CompletedAt)
}

func validateSample(s Sample) error {
	if err := validID(s.ID); err != nil {
		return err
	}
	if err := validID(s.BatchID); err != nil {
		return err
	}
	if s.ChallengeIndex < 0 || s.LatencyMS < 0 || s.InputTokens < 0 || s.OutputTokens < 0 {
		return invalid("negative sample counters")
	}
	if s.ResponseSHA256 != "" && s.ResponseSHA256 != sha([]byte(s.RawResponse)) {
		return invalid("response SHA256 mismatch")
	}
	if s.SystemPromptHash != "" && !validSHA(s.SystemPromptHash) {
		return invalid("invalid system prompt SHA256")
	}
	if err := validTimes(s.CollectedAt); err != nil {
		return err
	}
	for _, v := range []any{s.ParsedNumbers, s.Features, s.RequestParams, s.RouteEvidence} {
		if _, err := publicJSON(v); err != nil {
			return err
		}
	}
	return nil
}

func validateResult(r Result) error {
	if err := validID(r.ID); err != nil {
		return err
	}
	if err := validID(r.BatchID); err != nil {
		return err
	}
	for _, v := range []*float64{r.Top1Score, r.Top2Score, r.ScoreMargin, r.CalibratedProbability} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0)) {
			return invalid("non-finite score")
		}
	}
	if p := r.CalibratedProbability; p != nil && (*p < 0 || *p > 1) {
		return invalid("probability outside [0,1]")
	}
	if err := validTimes(r.CreatedAt); err != nil {
		return err
	}
	if _, err := publicJSON(r.Candidates); err != nil {
		return err
	}
	_, err := publicJSON(r.Metrics)
	return err
}

func validateTemplate(t Template) error {
	if err := validID(t.ID); err != nil {
		return err
	}
	if t.Version < 1 || t.PromptSHA256 != sha([]byte(t.Prompt)) {
		return invalid("invalid template version/prompt SHA256")
	}
	if err := validTimes(t.CreatedAt); err != nil {
		return err
	}
	_, err := publicJSON(t.Parameters)
	return err
}

func validateBank(b Bank) error {
	if err := validID(b.ID); err != nil {
		return err
	}
	if b.ModelCount < 0 || b.SampleCount < 0 || b.SHA256 != sha(b.Data) {
		return invalid("invalid bank counts/SHA256")
	}
	if err := checkJSON(b.Data, true); err != nil {
		return err
	}
	if len(b.Data) == 0 || (bytes.TrimSpace(b.Data)[0] != '{' && bytes.TrimSpace(b.Data)[0] != '[') {
		return invalid("bank data must be a JSON object/array")
	}
	if err := validTimes(b.CreatedAt); err != nil {
		return err
	}
	_, err := publicJSON(b.ValidationMetrics)
	return err
}

func publicJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", invalid("JSON encoding: %v", err)
	}
	if err = checkJSON(b, true); err != nil {
		return "", err
	}
	return string(b), nil
}

func credentialKey(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "", ".", "").Replace(key))
	switch key {
	case "authorization", "proxyauthorization", "apikey", "xapikey", "gatewaykey", "routekey", "password", "secret", "clientsecret", "privatekey", "accesstoken", "accesskey", "bearertoken", "token", "cookie", "setcookie":
		return true
	}
	return false
}

// The standard decoder otherwise accepts duplicate keys (last wins). Both
// backups and public JSON columns reject them, trailing input and deep nesting.
func checkJSON(b []byte, public bool) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 64 {
			return invalid("JSON nesting exceeds 64")
		}
		token, err := d.Token()
		if err != nil {
			return invalid("invalid JSON: %v", err)
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{':
				keys := map[string]bool{}
				for d.More() {
					keyToken, err := d.Token()
					if err != nil {
						return invalid("invalid object key")
					}
					key, ok := keyToken.(string)
					if !ok || keys[key] {
						return invalid("duplicate/invalid JSON key")
					}
					keys[key] = true
					if public && credentialKey(key) {
						return invalid("credential field is not public data")
					}
					if err = walk(depth + 1); err != nil {
						return err
					}
				}
				end, err := d.Token()
				if err != nil || end != json.Delim('}') {
					return invalid("invalid JSON object")
				}
			case '[':
				for d.More() {
					if err := walk(depth + 1); err != nil {
						return err
					}
				}
				end, err := d.Token()
				if err != nil || end != json.Delim(']') {
					return invalid("invalid JSON array")
				}
			default:
				return invalid("unexpected JSON delimiter")
			}
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return invalid("trailing JSON data")
	}
	return nil
}

func strictDecode(b []byte, v any) error {
	if err := checkJSON(b, false); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return invalid("invalid document fields: %v", err)
	}
	return nil
}
