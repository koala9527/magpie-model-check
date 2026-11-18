package fingerprint

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultChallenges(t *testing.T) {
	challenges := DefaultChallenges()
	if !reflect.DeepEqual(challenges, DefaultChallenges()) {
		t.Fatal("default challenges must be deterministic")
	}
	if len(challenges) != 3 {
		t.Fatalf("got %d challenges, want 3", len(challenges))
	}
	ids := map[string]bool{}
	for i, c := range challenges {
		if c.ID == "" || ids[c.ID] || c.Version != 1 || c.ParserVersion != ParserVersion {
			t.Fatalf("invalid versioned challenge: %+v", c)
		}
		ids[c.ID] = true
		if c.Min != 1 || c.Max != 355 || c.Count != []int{292, 312, 332}[i] {
			t.Fatalf("unexpected legacy range/count: %+v", c)
		}
		if !strings.Contains(c.Prompt, fmt.Sprintf("%d 个 1 到 355", c.Count)) ||
			!strings.Contains(c.Prompt, "禁止调用或借助任何工具") {
			t.Fatalf("legacy prompt contract lost: %q", c.Prompt)
		}
		if want := fmt.Sprintf("%x", sha256.Sum256([]byte(c.Prompt))); c.SHA256 != want {
			t.Fatalf("prompt digest got %q, want %q", c.SHA256, want)
		}
		if c.Parameters == nil || len(c.Parameters) != 0 {
			t.Fatal("preserve unspecified legacy sampling parameters with a fresh empty map")
		}
	}
	challenges[0].Parameters["temperature"] = 99
	if len(challenges[1].Parameters) != 0 || len(DefaultChallenges()[0].Parameters) != 0 {
		t.Fatal("parameters leaked between challenges/calls")
	}
}
