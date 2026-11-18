package fingerprint

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var numericToken = regexp.MustCompile(`^-?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// Parse accepts only ASCII unsigned integer tokens, whitespace and ASCII commas.
// It never extracts a valid-looking subsequence, filters values or returns a
// partial sample. A successful parse returns an empty reason. Failure returns
// nil and one of the versioned reason strings documented in the regression doc.
// Contract errors and explicit truncation take precedence over content errors;
// other errors are reported in input order, before checking the exact count.
func Parse(raw string, c Challenge, finishReason string) ([]int, string) {
	if c.Min < 1 || c.Max > 355 || c.Min > c.Max || c.Count <= 0 || c.ParserVersion != ParserVersion {
		return nil, "invalid_challenge"
	}
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "length", "max_tokens", "max_output_tokens", "token_limit", "incomplete":
		return nil, "truncated"
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "empty"
	}
	numbers := make([]int, 0, min(c.Count, 64))
	for _, segment := range strings.Split(raw, ",") {
		tokens := strings.Fields(segment)
		if len(tokens) == 0 {
			return nil, "invalid_format"
		}
		for _, token := range tokens {
			if numericToken.MatchString(token) {
				if token[0] == '-' {
					return nil, "negative"
				}
				if strings.ContainsAny(token, ".eE") {
					return nil, "decimal"
				}
			}
			integer := true
			for _, r := range token {
				if r < '0' || r > '9' {
					if unicode.IsLetter(r) || unicode.IsNumber(r) {
						return nil, "text"
					}
					integer = false
				}
			}
			if !integer {
				return nil, "invalid_format"
			}
			value, err := strconv.ParseUint(token, 10, 64)
			if err != nil || value < uint64(c.Min) || value > uint64(c.Max) {
				return nil, "out_of_range"
			}
			numbers = append(numbers, int(value))
		}
	}
	if len(numbers) != c.Count {
		return nil, "wrong_count"
	}
	return numbers, ""
}
