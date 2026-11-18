package attribution

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestWilsonIndependentBinomialIntervals(t *testing.T) {
	for _, tc := range []struct {
		events, total int
		lo, hi        float64
	}{
		{0, 10, 0, 0.2775327998628892},
		{5, 10, 0.236593090512564, 0.763406909487436},
		{10, 10, 0.7224672001371107, 1},
	} {
		m, err := Wilson(tc.events, tc.total, .95)
		if err != nil || m.Interval == nil || m.Value == nil {
			t.Fatalf("Wilson(%d,%d): %+v, %v", tc.events, tc.total, m, err)
		}
		if math.Abs(m.Interval.Lower-tc.lo) > 1e-12 || math.Abs(m.Interval.Upper-tc.hi) > 1e-12 {
			t.Errorf("Wilson(%d,%d) = %+v; want [%g,%g]", tc.events, tc.total, m.Interval, tc.lo, tc.hi)
		}
	}
}

func TestWilsonMissingAndInvalidData(t *testing.T) {
	m, err := Wilson(0, 0, .95)
	if err != nil || m.Value != nil || m.Interval != nil {
		t.Fatalf("missing data must be null, not zero: %+v, %v", m, err)
	}
	for _, tc := range []struct {
		events, total int
		confidence    float64
	}{{-1, 1, .95}, {2, 1, .95}, {0, -1, .95}, {0, 1, 0}, {0, 1, 1}, {0, 1, math.NaN()}, {0, 1, math.Inf(1)}} {
		if _, err := Wilson(tc.events, tc.total, tc.confidence); err == nil {
			t.Errorf("accepted invalid Wilson input %+v", tc)
		}
	}
}

func TestDecisionProbabilityIsNullByDefault(t *testing.T) {
	data, err := json.Marshal(Decision{Status: Inconclusive})
	if err != nil || !strings.Contains(string(data), `"calibrated_probability":null`) {
		t.Fatalf("default probability: %s, %v", data, err)
	}
}

func TestBuiltinLegacyNeverValidatesIdentity(t *testing.T) {
	e, r := BuiltinLegacy()
	if r.IdentityValidated || r.MechanicsValidated || r.ProvenanceValidated || r.CorpusSHA256 != "" {
		t.Fatalf("legacy bank falsely validated: %+v", r)
	}
	d := e.Decide(Request{ClaimedModel: "any", RouteVerified: true})
	if d.Status != Inconclusive || d.CalibratedProbability != nil || len(d.Reasons) == 0 {
		t.Fatalf("legacy verdict = %+v", d)
	}
}
