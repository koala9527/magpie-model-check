package attribution

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/koala9527/magpie-model-check/internal/fingerprint"
)

type group struct {
	id, model, family, batch, session, channel, day, systemPrompt string
	split                                                         Split
	features                                                      map[string][]float64
}

func readCorpusHeader(raw []byte) (Corpus, map[string]fingerprint.Challenge, error) {
	var c Corpus
	if err := strictJSON(raw, &c); err != nil {
		return c, nil, err
	}
	if c.SchemaVersion != SchemaVersion || c.AlgorithmVersion != fingerprint.AlgorithmVersion || (c.Kind != Real && c.Kind != Synthetic) || !validSHA(c.SystemPromptSHA256) {
		return c, nil, fmt.Errorf("unsupported corpus schema/algorithm/kind or missing system prompt binding")
	}
	if len(c.Challenges) == 0 {
		return c, nil, fmt.Errorf("challenge protocol missing")
	}
	challenges := map[string]fingerprint.Challenge{}
	for i, ch := range c.Challenges {
		if !identifier(ch.ID) || ch.Version <= 0 || ch.Prompt == "" || !validSHA(ch.SHA256) || ch.SHA256 != sha([]byte(ch.Prompt)) || ch.ParserVersion != fingerprint.ParserVersion || ch.Min < 1 || ch.Max > 355 || ch.Min > ch.Max || ch.Count <= 0 {
			return c, nil, fmt.Errorf("invalid challenge contract %q", ch.ID)
		}
		if _, exists := challenges[ch.ID]; exists {
			return c, nil, fmt.Errorf("duplicate challenge ID %q", ch.ID)
		}
		if ch.Parameters == nil {
			ch.Parameters = map[string]any{}
		}
		c.Challenges[i].Parameters = ch.Parameters
		challenges[ch.ID] = ch
	}
	return c, challenges, nil
}

func readGroups(c Corpus, models []Model, challenges map[string]fingerprint.Challenge) ([]group, []string, error) {
	known := map[string]string{}
	for _, m := range models {
		known[m.ID] = m.Family
	}
	groups := map[string]*group{}
	ids, requests, batches, sessions := map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	for _, r := range c.Records {
		if !identifier(r.ID) || !identifier(r.GroupID) || !identifier(r.BatchID) || !identifier(r.SessionID) || !identifier(r.Model) || !identifier(r.Family) || !identifier(r.Channel) || !identifier(r.RequestID) {
			return nil, nil, fmt.Errorf("record %q missing collection identity/label/channel/request", r.ID)
		}
		if !validSplit(r.Split) {
			return nil, nil, fmt.Errorf("record %q invalid split", r.ID)
		}
		if ids[r.ID] != "" {
			return nil, nil, fmt.Errorf("duplicate record %q", r.ID)
		}
		ids[r.ID] = r.GroupID
		requestKey := r.Channel + "\x00" + r.RequestID
		if _, exists := requests[requestKey]; exists {
			return nil, nil, fmt.Errorf("duplicate request %q", r.ID)
		}
		requests[requestKey] = r.ID
		for _, entry := range []struct {
			index map[string]string
			key   string
		}{{batches, r.BatchID}, {sessions, r.SessionID}} {
			if old := entry.index[entry.key]; old != "" && old != r.GroupID {
				return nil, nil, fmt.Errorf("collection dependence crosses group: %q", r.ID)
			}
			entry.index[entry.key] = r.GroupID
		}
		ts, err := time.Parse(time.RFC3339Nano, r.CollectedAt)
		if err != nil {
			return nil, nil, fmt.Errorf("record %q invalid collection timestamp", r.ID)
		}
		day := ts.UTC().Format("2006-01-02")
		if f, ok := known[r.Model]; ok && f != r.Family {
			return nil, nil, fmt.Errorf("record %q family differs from bank class", r.ID)
		}
		_, isKnown := known[r.Model]
		if (r.Split == Reference || r.Split == Test) && !isKnown || r.Split == UnknownTest && isKnown {
			return nil, nil, fmt.Errorf("record %q label incompatible with split", r.ID)
		}
		if c.Kind == Synthetic && r.SourceType != "synthetic" || c.Kind == Real && !realSource(r.SourceType) {
			return nil, nil, fmt.Errorf("record %q source incompatible with dataset kind", r.ID)
		}
		ch, ok := challenges[r.ChallengeID]
		if !ok {
			return nil, nil, fmt.Errorf("record %q unknown challenge", r.ID)
		}
		parameters, _ := json.Marshal(ch.Parameters)
		if !sameJSON(parameters, r.RequestParameters) || r.SystemPromptSHA256 != c.SystemPromptSHA256 {
			return nil, nil, fmt.Errorf("record %q sampling/system protocol mismatch", r.ID)
		}
		if !validSHA(r.ResponseSHA256) || r.ResponseSHA256 != sha([]byte(r.RawResponse)) {
			return nil, nil, fmt.Errorf("record %q raw response hash mismatch", r.ID)
		}
		for _, evidence := range []struct {
			raw  json.RawMessage
			hash string
		}{{r.LabelEvidence, r.LabelEvidenceSHA256}, {r.RouteEvidence, r.RouteEvidenceSHA256}} {
			if !validSHA(evidence.hash) || evidence.hash != sha(evidence.raw) {
				return nil, nil, fmt.Errorf("record %q evidence hash mismatch", r.ID)
			}
		}
		numbers, reason := fingerprint.Parse(r.RawResponse, ch, r.FinishReason)
		if reason != "" {
			return nil, nil, fmt.Errorf("record %q parse:%s", r.ID, reason)
		}
		features, err := fingerprint.Features(numbers)
		if err != nil {
			return nil, nil, fmt.Errorf("record %q features: %w", r.ID, err)
		}
		g := groups[r.GroupID]
		if g == nil {
			g = &group{id: r.GroupID, model: r.Model, family: r.Family, batch: r.BatchID, session: r.SessionID, channel: r.Channel, day: day, systemPrompt: r.SystemPromptSHA256, split: r.Split, features: map[string][]float64{}}
			groups[r.GroupID] = g
		}
		if g.model != r.Model || g.family != r.Family || g.batch != r.BatchID || g.session != r.SessionID || g.channel != r.Channel || g.day != day || g.split != r.Split {
			return nil, nil, fmt.Errorf("record %q group metadata or partition mismatch", r.ID)
		}
		if _, exists := g.features[r.ChallengeID]; exists {
			return nil, nil, fmt.Errorf("record %q repeats challenge in group", r.ID)
		}
		g.features[r.ChallengeID] = features
	}
	ordered := make([]group, 0, len(groups))
	for _, g := range groups {
		if len(g.features) != len(challenges) {
			return nil, nil, fmt.Errorf("group %q incomplete challenge protocol", g.id)
		}
		ordered = append(ordered, *g)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].id < ordered[j].id })
	reasons, err := validateSplits(ordered, models)
	return ordered, reasons, err
}

func validSplit(s Split) bool {
	return s == Reference || s == Calibration || s == Test || s == UnknownTest || s == ChannelHoldout || s == TimeHoldout
}
func realSource(s string) bool {
	return s == "official_api_reference" || s == "magpie_provider_reference" || s == "third_party_channel" || s == "unknown_test" || s == "manual_import"
}

func validateSplits(groups []group, models []Model) ([]string, error) {
	counts := map[Split]map[string]int{}
	known := map[string]bool{}
	for _, m := range models {
		known[m.ID] = true
	}
	channels, days := map[string]Split{}, map[string]Split{}
	calUnknown := map[string]bool{}
	maxOtherDay := ""
	for _, g := range groups {
		if counts[g.split] == nil {
			counts[g.split] = map[string]int{}
		}
		counts[g.split][g.model]++
		if old, ok := channels[g.channel]; ok && old != g.split && (old == ChannelHoldout || g.split == ChannelHoldout) {
			return nil, fmt.Errorf("channel holdout leaked: %q", g.channel)
		}
		if old, ok := days[g.day]; ok && old != g.split {
			return nil, fmt.Errorf("UTC date crosses partitions: %q", g.day)
		}
		channels[g.channel] = g.split
		days[g.day] = g.split
		if g.split != TimeHoldout {
			maxOtherDay = max(maxOtherDay, g.day)
		}
		if g.split == Calibration && !known[g.model] {
			calUnknown[g.model] = true
		}
	}
	for _, g := range groups {
		if g.split == TimeHoldout && g.day <= maxOtherDay {
			return nil, fmt.Errorf("time holdout is not forward independent: %q", g.day)
		}
		if g.split != Calibration && g.split != Reference && calUnknown[g.model] {
			return nil, fmt.Errorf("unknown model holdout used in calibration: %q", g.model)
		}
	}
	reasons := []string{}
	for _, s := range []Split{Reference, Calibration, Test, UnknownTest, ChannelHoldout, TimeHoldout} {
		if len(counts[s]) == 0 {
			reasons = append(reasons, "missing_partition:"+string(s))
		}
		if s != UnknownTest {
			for _, m := range models {
				if counts[s][m.ID] == 0 {
					reasons = append(reasons, "missing_model_partition:"+string(s)+":"+m.ID)
				}
			}
		}
	}
	if len(calUnknown) == 0 {
		reasons = append(reasons, "unknown_calibration_missing")
	}
	return reasons, nil
}
