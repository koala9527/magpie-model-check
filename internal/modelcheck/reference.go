package modelcheck

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/koala9527/magpie-model-check/internal/attribution"
	"github.com/koala9527/magpie-model-check/internal/fingerprint"
	"github.com/koala9527/magpie-model-check/internal/modelstore"
)

// Engines are immutable after loading. A batch retains its engine rather than
// looking up a mutable imported trusted flag part-way through collection.
type referenceEntry struct {
	id         string
	sha256     string
	engine     *attribution.Engine
	report     attribution.ValidationReport
	provenance json.RawMessage
	models     int
	samples    int
	compatible bool
}

func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (s *Service) containsPrivateValue(raw []byte) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	var inspect func(any, int) bool
	inspect = func(v any, depth int) bool {
		if depth > 64 {
			return true
		}
		switch child := v.(type) {
		case map[string]any:
			for key, item := range child {
				if inspect(key, depth+1) || inspect(item, depth+1) {
					return true
				}
			}
		case []any:
			for _, item := range child {
				if inspect(item, depth+1) {
					return true
				}
			}
		case string:
			for _, secret := range []string{s.config.GatewayKey, s.config.RouteKey, hex.EncodeToString(s.reviewKey)} {
				if secret != "" && (child == secret || len(secret) >= 16 && strings.Contains(child, secret) || strings.Contains(child, "Bearer "+secret)) {
					return true
				}
			}
			trim := strings.TrimSpace(child)
			if strings.HasPrefix(trim, "{") || strings.HasPrefix(trim, "[") {
				var nested any
				if json.Unmarshal([]byte(child), &nested) == nil {
					return inspect(nested, depth+1)
				}
			}
		}
		return false
	}
	return inspect(value, 0)
}

func (s *Service) refreshBuiltinReference() error {
	bank, err := s.store.Bank("builtin-modeltrace")
	if err != nil && !errors.Is(err, modelstore.ErrNotFound) {
		return err
	}
	created := bank.CreatedAt
	if created == "" {
		created = nowText()
	}
	return s.store.PutBank(modelstore.Bank{ID: "builtin-modeltrace", Version: LegacyBankVersion, AlgorithmVersion: fingerprint.AlgorithmVersion,
		ChallengeVersion: ChallengeVersion, SHA256: fingerprint.LegacyBankSHA256(), SourceType: "legacy_modeltrace",
		Provenance: "ModelTrace MIT / 6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2; historical closed-set centroids; source and calibration not independently validated",
		ModelCount: 18, Trusted: false, CreatedAt: created, Data: fingerprint.LegacyBankJSON(),
		ValidationMetrics: map[string]any{"independent_validation": false, "sample_count": "not established"}})
}

// The collector owns this protocol. Reference files never choose the prompt,
// request options or system message that we send through the user's channel.
func collectionChallenges() []fingerprint.Challenge {
	challenges := fingerprint.DefaultChallenges()
	for i := range challenges {
		parameters := map[string]any{"stream": false, "max_tokens": MaxOutputTokens}
		for key, value := range challenges[i].Parameters {
			if key != "model" && key != "messages" && key != "stream" && key != "max_tokens" && key != "_magpie_model_check_probe" {
				parameters[key] = value
			}
		}
		challenges[i].Parameters = parameters
	}
	return challenges
}

func collectionProtocolSHA256() string {
	hash, err := attribution.ProtocolSHA256(collectionChallenges(), emptySystemPromptHash())
	if err != nil {
		panic("invalid built-in collection protocol")
	}
	return hash
}

func loadReference(bank modelstore.Bank, options attribution.Options) (*referenceEntry, error) {
	entry := &referenceEntry{id: bank.ID, sha256: bank.SHA256, provenance: json.RawMessage(`[]`)}
	var envelope struct {
		Format string `json:"format"`
	}
	if bank.ID == "builtin-modeltrace" {
		entry.engine, entry.report = attribution.BuiltinLegacy()
		entry.models = 18
		return entry, nil
	}
	if err := json.Unmarshal(bank.Data, &envelope); err != nil {
		return nil, errors.New("INVALID_REFERENCE_JSON")
	}
	if envelope.Format != attribution.ReferenceBundleFormat {
		// Original/third-party raw resources are still exportable, but without a
		// bound raw experiment they cannot be reviewed or activated as identity.
		entry.report = attribution.ValidationReport{SchemaVersion: 1, BankVersion: bank.Version, BankSHA256: bank.SHA256,
			AlgorithmVersion: fingerprint.AlgorithmVersion, MethodVersion: attribution.MethodVersion,
			Reasons: []string{"raw_reference_corpus_missing", "independent_calibration_missing"}, ProbabilityReason: "probability_model_not_fitted"}
		return entry, nil
	}
	engine, report, err := attribution.LoadBundle(bank.Data, options)
	if err != nil {
		return nil, errors.New("INVALID_REFERENCE_EXPERIMENT")
	}
	entry.engine, entry.report = engine, report
	if report.BankVersion != bank.Version || report.AlgorithmVersion != bank.AlgorithmVersion {
		return nil, errors.New("REFERENCE_MANIFEST_MISMATCH")
	}
	entry.provenance, err = engine.ProvenanceJSON()
	if err != nil {
		return nil, errors.New("INVALID_REFERENCE_EXPERIMENT")
	}
	var records []attribution.Binding
	if json.Unmarshal(entry.provenance, &records) != nil {
		return nil, errors.New("INVALID_REFERENCE_EXPERIMENT")
	}
	var manifest attribution.Bank
	if json.Unmarshal(engine.RawBankJSON(), &manifest) == nil {
		entry.models = len(manifest.Models)
	}
	entry.samples = len(records)
	entry.compatible = report.ChallengeSetSHA256 == collectionProtocolSHA256()
	return entry, nil
}

func referenceMetadata(bank modelstore.Bank, entry *referenceEntry) modelstore.Bank {
	bank.Trusted = false
	bank.ValidationMetrics = map[string]any{"independent_validation": false, "review_status": "PENDING_SOURCE_REVIEW"}
	if entry != nil {
		bank.Trusted = entry.report.ProvenanceValidated && entry.report.DatasetKind == attribution.Real
		bank.ModelCount, bank.SampleCount = entry.models, entry.samples
		bank.ValidationMetrics = map[string]any{"independent_validation": entry.report.IdentityValidated,
			"identity_validated": entry.report.IdentityValidated, "protocol_compatible": entry.compatible, "validation": entry.report}
	}
	return bank
}

func (s *Service) inspectReference(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bank, err := s.store.Bank(r.PathValue("id"))
	if err != nil {
		apiError(w, 404, "NOT_FOUND", "参考库不存在")
		return
	}
	entry := s.references[bank.ID]
	bank = referenceMetadata(bank, entry)
	bank.Data = nil
	if entry == nil {
		apiError(w, 422, "INVALID_REFERENCE_EXPERIMENT", "参考资料未通过本机重新计算")
		return
	}
	apiJSON(w, 200, map[string]any{"bank": bank, "validation": entry.report, "provenance": entry.provenance,
		"provenance_sha256": digestBytes(entry.provenance), "protocol_compatible": entry.compatible,
		"local_review": s.audits.Reviews[bank.ID], "active": s.audits.ActiveBankID == bank.ID && s.activeReference() != nil})
}
