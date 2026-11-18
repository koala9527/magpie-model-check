package fingerprint

import (
	"reflect"
	"strings"
	"testing"
)

func parserChallenge() Challenge {
	return Challenge{Min: 1, Max: 355, Count: 3, ParserVersion: ParserVersion}
}

func TestParseStrict(t *testing.T) {
	cases := []struct {
		name, raw, finish, reason string
		want                      []int
	}{
		{name: "comma", raw: "1,2,355", finish: "stop", want: []int{1, 2, 355}},
		{name: "whitespace", raw: " \t1 2\r\n355\n", want: []int{1, 2, 355}},
		{name: "comma and whitespace", raw: "1, 2,\n355", want: []int{1, 2, 355}},
		{name: "repetition", raw: "7 7 7", finish: "end_turn", want: []int{7, 7, 7}},
		{name: "leading zero", raw: "001 2 355", want: []int{1, 2, 355}},
		{name: "empty", reason: "empty"},
		{name: "blank", raw: " \t\r\n", reason: "empty"},
		{name: "negative", raw: "1,-2,355", reason: "negative"},
		{name: "negative zero", raw: "1 -0 355", reason: "negative"},
		{name: "decimal", raw: "1 2.5 355", reason: "decimal"},
		{name: "trailing decimal point", raw: "1 2. 355", reason: "decimal"},
		{name: "leading decimal point", raw: "1 .2 355", reason: "decimal"},
		{name: "exponent", raw: "1 2e2 355", reason: "decimal"},
		{name: "zero", raw: "1 0 355", reason: "out_of_range"},
		{name: "high", raw: "1 356 355", reason: "out_of_range"},
		{name: "overflow", raw: "1 " + strings.Repeat("9", 100) + " 355", reason: "out_of_range"},
		{name: "text prefix", raw: "答案：1,2,355", reason: "text"},
		{name: "text middle", raw: "1 and 2 355", reason: "text"},
		{name: "text suffix", raw: "1 2 355 done", reason: "text"},
		{name: "unicode digits", raw: "１ 2 355", reason: "text"},
		{name: "code fence", raw: "```\n1 2 355\n```", reason: "invalid_format"},
		{name: "json array", raw: "[1,2,355]", reason: "invalid_format"},
		{name: "plus sign", raw: "1 +2 355", reason: "invalid_format"},
		{name: "double comma", raw: "1,,2,355", reason: "invalid_format"},
		{name: "leading comma", raw: ",1,2,355", reason: "invalid_format"},
		{name: "trailing comma", raw: "1,2,355,", reason: "invalid_format"},
		{name: "too few", raw: "1 355", reason: "wrong_count"},
		{name: "too many", raw: "1 2 3 355", reason: "wrong_count"},
		{name: "length even at exact count", raw: "1 2 355", finish: "length", reason: "truncated"},
		{name: "max tokens", raw: "1 2", finish: "max_tokens", reason: "truncated"},
		{name: "max output tokens", raw: "1 2 355", finish: "max_output_tokens", reason: "truncated"},
		{name: "token limit", raw: "1 2 355", finish: "token_limit", reason: "truncated"},
		{name: "incomplete", raw: "1 2 355", finish: "incomplete", reason: "truncated"},
		{name: "normalized length", raw: "1 2 355", finish: " LENGTH ", reason: "truncated"},
		{name: "empty truncated", finish: "length", reason: "truncated"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := Parse(tt.raw, parserChallenge(), tt.finish)
			if reason != tt.reason || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Parse(%q, finish=%q) = %v, %q; want %v, %q", tt.raw, tt.finish, got, reason, tt.want, tt.reason)
			}
		})
	}
}

func TestParseRejectsInvalidChallenge(t *testing.T) {
	for _, c := range []Challenge{
		{},
		{Min: 2, Max: 1, Count: 3, ParserVersion: ParserVersion},
		{Min: 0, Max: 355, Count: 3, ParserVersion: ParserVersion},
		{Min: 1, Max: 356, Count: 3, ParserVersion: ParserVersion},
		{Min: 1, Max: 355, Count: 0, ParserVersion: ParserVersion},
		{Min: 1, Max: 355, Count: 3, ParserVersion: "legacy"},
	} {
		if numbers, reason := Parse("1 2 355", c, "stop"); numbers != nil || reason != "invalid_challenge" {
			t.Fatalf("invalid contract accepted: %+v -> %v, %q", c, numbers, reason)
		}
	}
}

func TestParseHonorsChallengeSubset(t *testing.T) {
	c := Challenge{Min: 10, Max: 20, Count: 2, ParserVersion: ParserVersion}
	if numbers, reason := Parse("10 20", c, "stop"); reason != "" || !reflect.DeepEqual(numbers, []int{10, 20}) {
		t.Fatalf("subset contract failed: %v, %q", numbers, reason)
	}
	for _, raw := range []string{"9 20", "10 21"} {
		if numbers, reason := Parse(raw, c, "stop"); numbers != nil || reason != "out_of_range" {
			t.Fatalf("subset range ignored: %q -> %v, %q", raw, numbers, reason)
		}
	}
}

func TestParseFailurePrecedence(t *testing.T) {
	for _, tt := range []struct {
		raw, finish, reason string
		challenge           Challenge
	}{
		{"", "length", "invalid_challenge", Challenge{}},
		{"1 -2 355", "length", "truncated", parserChallenge()},
		{"1 -2.5 355", "stop", "negative", parserChallenge()},
		{"1 2.5 -3", "stop", "decimal", parserChallenge()},
		{"1 0 text", "stop", "out_of_range", parserChallenge()},
		{"text 0 355", "stop", "text", parserChallenge()},
		{",0 355", "stop", "invalid_format", parserChallenge()},
	} {
		if numbers, reason := Parse(tt.raw, tt.challenge, tt.finish); numbers != nil || reason != tt.reason {
			t.Fatalf("precedence for %q/%q: %v, %q; want %q", tt.raw, tt.finish, numbers, reason, tt.reason)
		}
	}
}
