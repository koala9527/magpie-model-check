package fingerprint

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyBankAccessors(t *testing.T) {
	original, err := os.ReadFile(filepath.Join("reference", "unified_bank.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := LegacyBankJSON()
	if !bytes.Equal(got, original) {
		t.Fatal("LegacyBankJSON must preserve original bytes, including line endings")
	}
	want := fmt.Sprintf("%x", sha256.Sum256(original))
	if LegacyBankSHA256() != want {
		t.Fatalf("raw bank digest=%s, want %s", LegacyBankSHA256(), want)
	}
	got[0] = 'x'
	if !bytes.Equal(LegacyBankJSON(), original) || LegacyBankSHA256() != want {
		t.Fatal("caller mutation changed the embedded bank or its digest")
	}
}

func TestLegacyReferenceIntegrity(t *testing.T) {
	for name, want := range map[string]string{
		"fingerprint-core.js": "83fa5bd611e18f8339122582335123c8ea168ed242298bb31f4e363abeeb6e4a",
		"unified_bank.json":   "f267d0513aa06981d024ec5394746fceddf44e4e8a75fa6bf8304255f4b32d6d",
		"LICENSE":             "238afb57e498990742f84b9ce6167b4e256ac06104a9938ce953bc41c14bea51",
	} {
		data, err := os.ReadFile(filepath.Join("reference", name))
		if err != nil {
			t.Fatal(err)
		}
		canonical := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
		if got := fmt.Sprintf("%x", sha256.Sum256(canonical)); got != want {
			t.Fatalf("pinned %s LF digest=%s, want %s", name, got, want)
		}
		if name != "LICENSE" {
			old, err := os.ReadFile(filepath.Join("..", "check", "web", "modeltrace", name))
			if err != nil || !bytes.Equal(data, old) {
				t.Fatalf("reference copy differs from untouched old asset %s: %v", name, err)
			}
		}
	}
}

func TestLegacyBankRejectsMalformedArtifacts(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte("{"), []byte("{}"), []byte("null")} {
		if bank, err := decodeBank(raw); err == nil || bank != nil {
			t.Fatalf("decodeBank(%q) = %v, %v; want nil and error", raw, bank, err)
		}
	}
	cases := []struct {
		name   string
		mutate func(*referenceBank)
	}{
		{"model order", func(b *referenceBank) { b.Robust.ModelOrder[0] = "wrong-model" }},
		{"duplicate model", func(b *referenceBank) { b.Models[1].ID = b.Models[0].ID }},
		{"missing counts", func(b *referenceBank) { b.Models[0].Counts = nil }},
		{"negative counts", func(b *referenceBank) { b.Models[0].Counts[0] = -1 }},
		{"mean dimension", func(b *referenceBank) { b.Robust.Hellinger.FeatureMean = []float64{0} }},
		{"zero scale", func(b *referenceBank) { b.Robust.Hellinger.FeatureScale[0] = 0 }},
		{"negative scale", func(b *referenceBank) { b.Robust.Hellinger.FeatureScale[0] = -1 }},
		{"basis dimension", func(b *referenceBank) { b.Robust.Hellinger.NuisanceBasis[0] = []float64{1} }},
		{"centroid dimension", func(b *referenceBank) { b.Robust.Hellinger.Centroids[0] = nil }},
		{"centroid count", func(b *referenceBank) { b.Robust.Hellinger.Centroids = nil }},
		{"ordered dimension", func(b *referenceBank) { b.Robust.OrderedBlocks.FeatureMean = nil }},
		{"missing environments", func(b *referenceBank) { b.Robust.OrderedBlocks.EnvironmentCentroids = nil }},
		{"environment count", func(b *referenceBank) { b.Robust.OrderedBlocks.EnvironmentCentroids[0] = nil }},
		{"environment dimension", func(b *referenceBank) { b.Robust.OrderedBlocks.EnvironmentCentroids[0][0] = nil }},
		{"invalid weight", func(b *referenceBank) { b.Robust.OrderedBlocks.Weight = 2 }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			bank, err := decodeBank(LegacyBankJSON())
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(bank)
			raw, err := json.Marshal(bank)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := decodeBank(raw); err == nil || got != nil {
				t.Fatalf("malformed %s accepted", tt.name)
			}
		})
	}
}
