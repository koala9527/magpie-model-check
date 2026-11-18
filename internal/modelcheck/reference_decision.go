package modelcheck

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/koala9527/magpie-model-check/internal/attribution"
	"github.com/koala9527/magpie-model-check/internal/modelstore"
)

func applyReferenceDecision(result *modelstore.Result, target modelstore.Target, samples []modelstore.Sample, entry *referenceEntry) {
	if entry == nil || entry.engine == nil {
		return
	}
	result.Metrics["reference_snapshot"] = map[string]any{"bank_id": entry.id, "bundle_sha256": entry.sha256,
		"bank_sha256": entry.report.BankSHA256, "corpus_sha256": entry.report.CorpusSHA256, "method_version": entry.report.MethodVersion,
		"validation": entry.report}
	result.Metrics["bank_trusted"] = entry.report.ProvenanceValidated
	result.Metrics["calibration_available"] = entry.report.IdentityValidated
	result.Metrics["identity_validated"] = false
	protocolVerified := len(samples) > 0
	for _, sample := range samples {
		protocolVerified = protocolVerified && sample.RouteEvidence["authenticated"] == true && sample.RouteEvidence["protocol_verified"] == true
	}
	result.Metrics["protocol_verified"] = protocolVerified
	if result.IdentityStatus == "UNAVAILABLE" || result.IdentityStatus == "ROUTE_UNVERIFIED" {
		return
	}
	if !protocolVerified {
		result.IdentityStatus, result.RejectionReason = "INCONCLUSIVE", "REQUEST_PROTOCOL_UNVERIFIED"
		return
	}
	challenges := collectionChallenges()
	if len(samples) < len(challenges) || len(samples)%len(challenges) != 0 {
		result.IdentityStatus, result.RejectionReason = "INCONCLUSIVE", "INCOMPLETE_CHALLENGE_ROUNDS"
		return
	}
	decisions := []attribution.Decision{}
	for offset := 0; offset < len(samples); offset += len(challenges) {
		group := samples[offset : offset+len(challenges)]
		channel, channelOK := group[0].RouteEvidence["provider_id"].(string)
		providerModel, modelOK := group[0].RouteEvidence["provider_model"].(string)
		if !channelOK || !modelOK || strings.TrimSpace(channel) == "" || strings.TrimSpace(providerModel) == "" {
			result.IdentityStatus, result.RejectionReason = "INCONCLUSIVE", "INCONSISTENT_CHALLENGE_ROUTE"
			return
		}
		day := ""
		request := attribution.Request{ClaimedModel: target.ClaimedModel, BankVersion: entry.report.BankVersion,
			AlgorithmVersion: entry.report.AlgorithmVersion, ChallengeSetSHA256: collectionProtocolSHA256(),
			Channel: channel, CollectedAt: group[0].CollectedAt, RouteVerified: true, SystemPromptSHA256: emptySystemPromptHash()}
		for _, sample := range group {
			collectedAt, err := time.Parse(time.RFC3339Nano, sample.CollectedAt)
			if err != nil {
				result.IdentityStatus, result.RejectionReason = "INCONCLUSIVE", "INVALID_SAMPLE_TIMESTAMP"
				return
			}
			sampleDay := collectedAt.UTC().Format("2006-01-02")
			if day != "" && sampleDay != day {
				result.IdentityStatus, result.RejectionReason = "INCONCLUSIVE", "CROSS_DAY_CHALLENGE_ROUND"
				return
			}
			day = sampleDay
			if sample.RouteEvidence["provider_id"] != channel || sample.RouteEvidence["provider_model"] != providerModel {
				result.IdentityStatus, result.RejectionReason = "INCONCLUSIVE", "INCONSISTENT_CHALLENGE_ROUTE"
				return
			}
			params, _ := json.Marshal(sample.RequestParams["parameters"])
			finish, _ := sample.RequestParams["finish_reason"].(string)
			request.Observations = append(request.Observations, attribution.Observation{ChallengeID: sample.ChallengeID,
				RawResponse: sample.RawResponse, FinishReason: finish, RequestParameters: params})
		}
		decisions = append(decisions, entry.engine.Decide(request))
	}
	result.Metrics["round_decisions"] = decisions
	result.Metrics["complete_rounds"] = len(decisions)
	first := decisions[0]
	result.IdentityStatus, result.RejectionReason = string(first.Status), strings.Join(first.Reasons, "; ")
	for _, decision := range decisions {
		if decision.Status != first.Status || decision.PredictedModel != first.PredictedModel || decision.PredictedFamily != first.PredictedFamily {
			result.IdentityStatus, result.RejectionReason = "INCONCLUSIVE", "INCONSISTENT_CHALLENGE_ROUNDS"
			break
		}
	}
	if target.ExpectedFamily != "" && first.PredictedFamily != "" && target.ExpectedFamily != first.PredictedFamily {
		result.IdentityStatus, result.RejectionReason = "INCONCLUSIVE", "DECLARED_FAMILY_CONFLICT"
	}
	if result.IdentityStatus != "INCONCLUSIVE" {
		result.PredictedModel, result.PredictedFamily = first.PredictedModel, first.PredictedFamily
		result.Metrics["identity_validated"] = first.IdentityValidated
		result.RejectionReason = map[string]string{"MATCHED": "BEHAVIOR_MATCHES_CLAIM", "MISMATCHED": "BEHAVIOR_MATCHES_OTHER_REFERENCE", "UNKNOWN": "BEHAVIOR_OUTSIDE_REFERENCE_CLASSES"}[result.IdentityStatus]
	}
	if len(first.Candidates) > 0 {
		result.Candidates = []map[string]any{}
		for _, candidate := range first.Candidates {
			result.Candidates = append(result.Candidates, map[string]any{"model": candidate.Model, "family": candidate.Family,
				"score": candidate.Score, "distance": candidate.Distance, "consistency": candidate.Consistency})
		}
		result.Metrics["candidate_method"] = entry.report.MethodVersion
		result.Top1Score = &first.Candidates[0].Score
		if len(first.Candidates) > 1 {
			result.Top2Score = &first.Candidates[1].Score
			margin := first.Candidates[0].Score - first.Candidates[1].Score
			result.ScoreMargin = &margin
		}
	}
}
