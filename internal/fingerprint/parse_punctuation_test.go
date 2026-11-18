package fingerprint

import "testing"

func TestParseRejectsTextBehindPunctuation(t *testing.T) {
	for _, raw := range []string{"1 2 355（完成）", "(answer)1 2 355"} {
		if numbers, reason := Parse(raw, parserChallenge(), "stop"); numbers != nil || reason != "text" {
			t.Errorf("Parse(%q) = %v, %q; want nil, text", raw, numbers, reason)
		}
	}
}
