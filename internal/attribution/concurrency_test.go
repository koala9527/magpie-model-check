package attribution

import (
	"bytes"
	"sync"
	"testing"
)

func TestConcurrentReadOnlyEngineAndDefensiveReports(t *testing.T) {
	e, r, c := calibratedFixture(t, 8)
	q := fixtureRequest(c, r, "1 1 1 1", "1 1 1 1", "synthetic/A-v1")
	originalBank := e.RawBankJSON()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 2; j++ {
				report := e.Report()
				report.IdentityValidated = true
				report.Policy.MaxFalseMatchRate = 1
				th := report.Thresholds["synthetic/A-v1"]
				th.MaxChallengeDistance[c.Challenges[0].ID] = 999
				report.BySplit[Test].Confusion["synthetic/A-v1"]["synthetic/A-v1"] = 999
				if d := e.Decide(q); d.Status != Inconclusive || d.IdentityValidated {
					t.Errorf("caller changed readonly validation state: %+v", d)
				}
				if data, err := e.ProvenanceJSON(); err != nil || !bytes.Contains(data, []byte(r.CorpusSHA256)) {
					t.Errorf("concurrent provenance export: %v", err)
				}
				copy := e.RawBankJSON()
				copy[0] = '!'
			}
		}()
	}
	wg.Wait()
	if !bytes.Equal(originalBank, e.RawBankJSON()) || e.Report().Thresholds["synthetic/A-v1"].MaxChallengeDistance[c.Challenges[0].ID] == 999 || e.Report().BySplit[Test].Confusion["synthetic/A-v1"]["synthetic/A-v1"] == 999 {
		t.Fatal("returned copies leaked shared state")
	}
}
