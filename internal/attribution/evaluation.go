package attribution

import (
	"fmt"
	"sort"
	"strings"
)

// The trial unit is a whole group. FMR is the conservative any-wrong-known-
// acceptance rate per group (not pairwise claims counted as independent).
// Replacement counts wrong accepted identities among known groups. Rejection
// includes INCONCLUSIVE so abstentions cannot inflate apparent performance.
type tally struct {
	groups, known, unknown, top1, family, falseMatch, replacement, unknownRecall, rejection int
	confusion                                                                               map[string]map[string]int
}

func (t *tally) add(e *Engine, g group) {
	if t.confusion == nil {
		t.confusion = map[string]map[string]int{}
	}
	d := e.classify(g.features)
	known := false
	for _, m := range e.bank.Models {
		if m.ID == g.model {
			known = true
		}
	}
	accepted := d.Status == Matched
	t.groups++
	if accepted && d.PredictedModel != g.model {
		t.falseMatch++
	}
	if known {
		t.known++
		if len(d.Candidates) >= 2 {
			best := d.Candidates[0]
			if best.Distance < d.Candidates[1].Distance && best.Model == g.model {
				t.top1++
			}
			familyUnique := true
			for _, other := range d.Candidates[1:] {
				if other.Distance == best.Distance && other.Family != best.Family {
					familyUnique = false
				}
			}
			if familyUnique && best.Family == g.family {
				t.family++
			}
		}
		if accepted && d.PredictedModel != g.model {
			t.replacement++
		}
		if !accepted {
			t.rejection++
		}
	} else {
		t.unknown++
		if d.Status == Unknown {
			t.unknownRecall++
		}
	}
	pred := string(d.Status)
	if accepted {
		pred = d.PredictedModel
	}
	if t.confusion[g.model] == nil {
		t.confusion[g.model] = map[string]int{}
	}
	t.confusion[g.model][pred]++
}

func measured(events, total int, confidence float64) Metric {
	if confidence > 0 {
		m, _ := Wilson(events, total, confidence)
		return m
	}
	m := Metric{Events: events, Total: total}
	if total > 0 {
		value := float64(events) / float64(total)
		m.Value = &value
	}
	return m
}

func (t *tally) metrics(confidence float64) Metrics {
	return Metrics{Groups: t.groups, Top1Accuracy: measured(t.top1, t.known, confidence), FamilyAccuracy: measured(t.family, t.known, confidence),
		FalseMatchRate: measured(t.falseMatch, t.groups, confidence), FalseReplacementRate: measured(t.replacement, t.known, confidence),
		UnknownRecall: measured(t.unknownRecall, t.unknown, confidence), KnownRejectionRate: measured(t.rejection, t.known, confidence), Confusion: t.confusion}
}

func (e *Engine) evaluate() {
	confidence := 0.0
	if e.report.Policy != nil {
		confidence = e.report.Policy.Confidence
	}
	bySplit := map[Split]*tally{}
	byModel := map[string]*tally{}
	byChannel := map[string]*tally{}
	byDate := map[string]*tally{}
	byModelSplit := map[Split]map[string]*tally{}
	for _, g := range e.groups {
		if bySplit[g.split] == nil {
			bySplit[g.split] = &tally{}
		}
		bySplit[g.split].add(e, g)
		if g.split == Reference || g.split == Calibration {
			continue
		}
		for _, entry := range []struct {
			index map[string]*tally
			key   string
		}{{byModel, g.model}, {byChannel, g.channel}, {byDate, g.day}} {
			if entry.index[entry.key] == nil {
				entry.index[entry.key] = &tally{}
			}
			entry.index[entry.key].add(e, g)
		}
		if byModelSplit[g.split] == nil {
			byModelSplit[g.split] = map[string]*tally{}
		}
		if byModelSplit[g.split][g.model] == nil {
			byModelSplit[g.split][g.model] = &tally{}
		}
		byModelSplit[g.split][g.model].add(e, g)
	}
	e.report.BySplit = map[Split]Metrics{}
	for _, s := range []Split{Reference, Calibration, Test, UnknownTest, ChannelHoldout, TimeHoldout} {
		if bySplit[s] == nil {
			bySplit[s] = &tally{}
		}
		e.report.BySplit[s] = bySplit[s].metrics(confidence)
	}
	e.report.ByModel = map[string]Metrics{}
	for key, t := range byModel {
		e.report.ByModel[key] = t.metrics(confidence)
	}
	e.report.ByChannel = map[string]Metrics{}
	for key, t := range byChannel {
		e.report.ByChannel[key] = t.metrics(confidence)
	}
	e.report.ByDate = map[string]Metrics{}
	for key, t := range byDate {
		e.report.ByDate[key] = t.metrics(confidence)
	}
	e.report.ByModelSplit = map[Split]map[string]Metrics{}
	for s, index := range byModelSplit {
		e.report.ByModelSplit[s] = map[string]Metrics{}
		for id, t := range index {
			e.report.ByModelSplit[s][id] = t.metrics(confidence)
		}
	}
	if e.report.Policy == nil {
		return
	}
	p := *e.report.Policy
	failures := []string{}
	for _, s := range []Split{Calibration, Test, UnknownTest, ChannelHoldout, TimeHoldout} {
		failures = append(failures, metricFailures(string(s), e.report.BySplit[s], p, s != UnknownTest, s != Test)...)
		if s == UnknownTest {
			continue
		}
		for _, m := range e.bank.Models {
			var metrics Metrics
			if s == Calibration {
				t := &tally{}
				for _, g := range e.groups {
					if g.split == s && g.model == m.ID {
						t.add(e, g)
					}
				}
				metrics = t.metrics(confidence)
			} else {
				metrics = e.report.ByModelSplit[s][m.ID]
			}
			failures = append(failures, metricFailures(string(s)+":"+m.ID, metrics, p, true, false)...)
		}
	}
	channels := make([]string, 0, len(e.report.ByChannel))
	for key := range e.report.ByChannel {
		channels = append(channels, key)
	}
	sort.Strings(channels)
	for _, channel := range channels {
		failures = append(failures, metricFailures("channel:"+channel, e.report.ByChannel[channel], p, true, true)...)
	}
	e.report.Reasons = append(e.report.Reasons, failures...)
	mechanicsOK := len(failures) == 0
	for _, reason := range e.report.Reasons {
		if reason != "synthetic_mechanics_only" && !strings.HasPrefix(reason, "provenance_") {
			mechanicsOK = false
		}
	}
	e.report.MechanicsValidated = mechanicsOK
	e.report.IdentityValidated = mechanicsOK && e.corpus.Kind == Real && e.report.ProvenanceValidated
}

func metricFailures(scope string, m Metrics, p Policy, known, unknown bool) []string {
	checks := []struct {
		name   string
		metric Metric
		target float64
		upper  bool
	}{
		{"false_match_rate", m.FalseMatchRate, p.MaxFalseMatchRate, true},
	}
	if known {
		checks = append(checks,
			struct {
				name   string
				metric Metric
				target float64
				upper  bool
			}{"false_replacement_rate", m.FalseReplacementRate, p.MaxFalseReplacementRate, true},
			struct {
				name   string
				metric Metric
				target float64
				upper  bool
			}{"known_class_rejection_rate", m.KnownRejectionRate, p.MaxKnownRejectionRate, true},
			struct {
				name   string
				metric Metric
				target float64
				upper  bool
			}{"top1_accuracy", m.Top1Accuracy, p.MinTop1Accuracy, false},
			struct {
				name   string
				metric Metric
				target float64
				upper  bool
			}{"family_accuracy", m.FamilyAccuracy, p.MinFamilyAccuracy, false})
	}
	if unknown {
		checks = append(checks, struct {
			name   string
			metric Metric
			target float64
			upper  bool
		}{"unknown_recall", m.UnknownRecall, p.MinUnknownRecall, false})
	}
	failures := []string{}
	for _, check := range checks {
		if check.metric.Interval == nil {
			failures = append(failures, "validation_population_missing:"+scope+"."+check.name)
			continue
		}
		value := check.metric.Interval.Lower
		bound := "lower"
		failed := value < check.target
		if check.upper {
			value = check.metric.Interval.Upper
			bound = "upper"
			failed = value > check.target
		}
		if failed {
			failures = append(failures, fmt.Sprintf("validation_target_not_met:%s.%s.%s=%g,target=%g", scope, check.name, bound, value, check.target))
		}
	}
	return failures
}
