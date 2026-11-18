package attribution

import (
	"encoding/json"
	"time"

	"github.com/koala9527/magpie-model-check/internal/fingerprint"
)

// decideMechanics deliberately cannot assert IdentityValidated. Synthetic unit
// tests use it to exercise decisions without creating a production bypass.
func (e *Engine) decideMechanics(request Request) Decision {
	if e == nil {
		return Decision{Status: Inconclusive, Reasons: []string{"engine_missing"}, ExpectedModel: request.ClaimedModel}
	}
	d := Decision{Status: Inconclusive, Reasons: []string{}, ExpectedModel: request.ClaimedModel,
		BankVersion: e.report.BankVersion, AlgorithmVersion: e.report.AlgorithmVersion, MethodVersion: MethodVersion,
		CorpusSHA256: e.report.CorpusSHA256, DatasetKind: e.report.DatasetKind}
	if !request.RouteVerified {
		d.Reasons = append(d.Reasons, "route_unverified")
	}
	if request.BankVersion != e.report.BankVersion {
		d.Reasons = append(d.Reasons, "bank_version_mismatch")
	}
	if request.AlgorithmVersion != e.report.AlgorithmVersion {
		d.Reasons = append(d.Reasons, "algorithm_version_mismatch")
	}
	if request.ChallengeSetSHA256 != e.report.ChallengeSetSHA256 {
		d.Reasons = append(d.Reasons, "challenge_protocol_mismatch")
	}
	if request.SystemPromptSHA256 != e.corpus.SystemPromptSHA256 {
		d.Reasons = append(d.Reasons, "system_prompt_mismatch")
	}
	claimKnown := false
	for _, m := range e.bank.Models {
		if m.ID == request.ClaimedModel {
			claimKnown = true
		}
	}
	if !claimKnown {
		d.Reasons = append(d.Reasons, "claimed_model_not_in_bank")
	}
	channelOK, dayOK, channelDayOK := false, false, false
	ts, err := time.Parse(time.RFC3339Nano, request.CollectedAt)
	for _, g := range e.groups {
		if g.split == Reference || g.split == Calibration {
			continue
		}
		if g.channel == request.Channel {
			channelOK = true
		}
		if err == nil && g.day == ts.UTC().Format("2006-01-02") {
			dayOK = true
			if g.channel == request.Channel {
				channelDayOK = true
			}
		}
	}
	if !channelOK {
		d.Reasons = append(d.Reasons, "channel_not_validated")
	}
	if !dayOK {
		d.Reasons = append(d.Reasons, "date_not_validated")
	}
	if channelOK && dayOK && !channelDayOK {
		d.Reasons = append(d.Reasons, "channel_date_not_validated")
	}
	features := map[string][]float64{}
	if len(request.Observations) != len(e.challenges) || len(e.challenges) == 0 {
		d.Reasons = append(d.Reasons, "incomplete_challenge_set")
	}
	for _, obs := range request.Observations {
		ch, ok := e.challenges[obs.ChallengeID]
		if !ok {
			d.Reasons = append(d.Reasons, "unknown_challenge:"+obs.ChallengeID)
			continue
		}
		if _, exists := features[obs.ChallengeID]; exists {
			d.Reasons = append(d.Reasons, "duplicate_challenge")
			continue
		}
		params, _ := json.Marshal(ch.Parameters)
		requestParameters := request.RequestParameters
		if len(obs.RequestParameters) > 0 {
			requestParameters = obs.RequestParameters
		}
		if !sameJSON(params, requestParameters) {
			d.Reasons = append(d.Reasons, "request_parameters_mismatch")
		}
		numbers, reason := fingerprint.Parse(obs.RawResponse, ch, obs.FinishReason)
		if reason != "" {
			d.Reasons = append(d.Reasons, "invalid_sample:"+obs.ChallengeID+":"+reason)
			continue
		}
		f, err := fingerprint.Features(numbers)
		if err != nil {
			d.Reasons = append(d.Reasons, "invalid_features:"+obs.ChallengeID)
			continue
		}
		features[obs.ChallengeID] = f
	}
	if len(features) != len(e.challenges) {
		d.Reasons = append(d.Reasons, "incomplete_challenge_set")
	}
	if len(d.Reasons) > 0 {
		return d
	}
	result := e.classify(features)
	d.Candidates = result.Candidates
	d.Status = result.Status
	d.Reasons = result.Reasons
	d.PredictedModel = result.PredictedModel
	d.PredictedFamily = result.PredictedFamily
	if d.Status == Matched && d.PredictedModel != request.ClaimedModel {
		d.Status = Mismatched
	}
	return d
}
