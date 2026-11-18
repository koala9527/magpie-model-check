package modelcheck

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/koala9527/magpie-model-check/internal/modelstore"
)

// Inspect all JSON tokens, not just the last value retained for duplicate keys.
func privacyReviewHasCredential(raw []byte, secrets []string) bool {
	var scan func(string, int) bool
	scan = func(text string, depth int) bool {
		if depth > 64 {
			return true
		}
		decoder := json.NewDecoder(strings.NewReader(text))
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				return false
			}
			if err != nil {
				return true
			}
			value, ok := token.(string)
			if !ok {
				continue
			}
			for _, secret := range secrets {
				if secret != "" && strings.Contains(value, secret) {
					return true
				}
			}
			trim := strings.TrimSpace(value)
			if (strings.HasPrefix(trim, "{") || strings.HasPrefix(trim, "[")) && json.Valid([]byte(value)) && scan(value, depth+1) {
				return true
			}
		}
	}
	return scan(string(raw), 0)
}

func TestPrivacyReviewReflectedSampleFields(t *testing.T) {
	for _, key := range []string{"gateway", "route", "review"} {
		for _, field := range []string{"finish_reason", "response_model", "provider_id", "provider_model", "target_model", "raw_response", "nested_parameters", "nested_json_duplicates", "invalid_signature"} {
			t.Run(key+"/"+field, func(t *testing.T) {
				var secret, provider, originalText string
				var expectedOriginalProtocol bool
				s := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var packet map[string]any
					decoder := json.NewDecoder(r.Body)
					decoder.UseNumber()
					if err := decoder.Decode(&packet); err != nil {
						t.Error(err)
						return
					}
					id := packet["_magpie_model_check_probe"].(map[string]any)["id"].(string)
					finish, model := "stop", "untrusted-self-label"
					text := strings.TrimSuffix(strings.Repeat("17,", collectionChallenges()[0].Count), ",")
					if field == "finish_reason" {
						finish = "reflected-" + secret
					}
					if field == "response_model" {
						model = "reflected-" + secret
					}
					if field == "raw_response" || field == "invalid_signature" {
						text = secret
					}
					originalText = text
					route := signedRoute(strings.Repeat("a", 64), id, packet["model"].(string), provider, text, finish)
					route.Version = 3
					route.RequestSHA256 = requestBodySHA256(packet)
					if field == "provider_model" {
						route.ProviderModel = "reflected-" + secret
					}
					reply := map[string]any{"model": model, "choices": []any{map[string]any{
						"message": map[string]string{"content": text}, "finish_reason": finish}}}
					envelope, _ := json.Marshal(reply)
					route.EnvelopeSHA256 = digestBytes(envelope)
					mac := hmac.New(sha256.New, []byte(strings.Repeat("a", 64)))
					mac.Write([]byte(route.payload()))
					route.MAC = hex.EncodeToString(mac.Sum(nil))
					if field == "invalid_signature" {
						route.MAC = strings.Repeat("0", 64)
					}
					reply["_magpie_model_check_route"] = route
					response, _ := json.Marshal(reply)
					expectedOriginalProtocol = verifyRoute(route, strings.Repeat("a", 64), id, packet["model"].(string), provider, text, finish) &&
						verifyResponseEnvelope(route, response) && route.RequestSHA256 == requestBodySHA256(packet)
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(reply)
				}))
				s.config.ProtocolGuard = true
				switch key {
				case "gateway":
					secret = s.config.GatewayKey
				case "route":
					secret = s.config.RouteKey
				case "review":
					secret = hex.EncodeToString(s.reviewKey)
				}
				target := addTestTarget(t, s)
				batch := modelstore.Batch{ID: "privacy-review", TargetID: target.ID, Status: "PENDING", StartedAt: nowText()}
				if err := s.store.PutBatch(batch); err != nil {
					t.Fatal(err)
				}
				// Only the probe's snapshot is contaminated; the saved target stays clean.
				if field == "provider_id" {
					target.ProviderID = "reflected-" + secret
				}
				if field == "target_model" {
					target.ModelID = "reflected-" + secret
				}
				provider = target.ProviderID
				challenge := collectionChallenges()[0]
				if field == "nested_parameters" || field == "nested_json_duplicates" {
					challenge.Parameters["seed"] = int64(9007199254740993)
					encoded, _ := json.Marshal(map[string]string{"echo": secret})
					escaped := strings.ReplaceAll(string(encoded), secret[:1], "\\u00"+hex.EncodeToString([]byte(secret[:1])))
					extra := map[string]any{"echo-" + secret: []map[string]string{{"value": secret}}, "nested_json": escaped}
					if field == "nested_json_duplicates" {
						var escapedSecret strings.Builder
						for _, char := range secret {
							fmt.Fprintf(&escapedSecret, `\u%04x`, char)
						}
						extra["nested_duplicate_json"] = `{"echo":"` + escapedSecret.String() + `","echo":"public"}`
					}
					challenge.Parameters["extra"] = []any{extra}
				}
				sample, reason := s.probe(context.Background(), target, batch.ID, challenge, 0, 3)
				if reason != "" {
					t.Fatalf("probe failed instead of retaining sanitized evidence: %s", reason)
				}
				if sample.Valid || len(sample.ParsedNumbers) != 0 || len(sample.Features) != 0 || sample.InvalidReason != "REDACTED_CREDENTIAL_IN_SAMPLE" {
					t.Error("a credential-bearing sample remained usable or lost its redaction reason")
				}
				wantOriginalAuth := field != "invalid_signature"
				if sample.RouteEvidence["original_response_authenticated"] != wantOriginalAuth || sample.RouteEvidence["authenticated"] != false || sample.RouteEvidence["protocol_verified"] != false {
					t.Error("redaction did not distinguish original proof from sanitized evidence")
				}
				if sample.RouteEvidence["original_protocol_verified"] != expectedOriginalProtocol {
					t.Error("original protocol proof was not retained honestly")
				}
				wantOriginalValid := field != "raw_response" && field != "invalid_signature"
				if sample.RouteEvidence["original_parse_valid"] != wantOriginalValid {
					t.Error("original answer parse status was not retained honestly")
				}
				wantOriginalReason := ""
				if !wantOriginalValid {
					wantOriginalReason = "text"
				}
				if sample.RouteEvidence["original_invalid_reason"] != wantOriginalReason {
					t.Error("original parse failure was not retained")
				}
				if wantOriginalValid && sample.RawResponse != originalText {
					t.Error("metadata redaction changed a clean original answer")
				}
				if field == "nested_parameters" || field == "nested_json_duplicates" {
					parameters := sample.RequestParams["parameters"].(map[string]any)
					if parameters["seed"] != json.Number("9007199254740993") {
						t.Error("metadata redaction rounded a clean request parameter")
					}
				}
				if err := s.store.PutSample(sample); err != nil {
					t.Fatal(err)
				}
				saved, err := s.store.Samples(batch.ID)
				if err != nil || len(saved) != 1 {
					t.Fatal("sanitized sample was not persisted", err)
				}
				if saved[0].ResponseSHA256 != digestBytes([]byte(saved[0].RawResponse)) {
					t.Error("stored response digest does not describe the sanitized answer")
				}
				raw, err := s.store.Export()
				if err != nil {
					t.Fatal(err)
				}
				if privacyReviewHasCredential(raw, []string{s.config.GatewayKey, s.config.RouteKey, hex.EncodeToString(s.reviewKey)}) {
					t.Error("a known credential survived sample persistence/export")
				}
			})
		}
	}
}

func TestPrivacyReviewNumericCredentialKeepsOriginalParseStatus(t *testing.T) {
	s := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { syntheticReply(w, r, true) }))
	s.config.GatewayKey = "17,17,17,17"
	target := addTestTarget(t, s)
	sample, reason := s.probe(context.Background(), target, "numeric-credential", collectionChallenges()[0], 0, 3)
	if reason != "" || sample.Valid || sample.RouteEvidence["original_parse_valid"] != true || sample.RouteEvidence["original_invalid_reason"] != "" {
		t.Fatal("privacy processing reinterpreted an originally valid numeric answer")
	}
	if sample.RouteEvidence["original_response_authenticated"] != true || sample.RouteEvidence["authenticated"] != false {
		t.Fatal("redacted numeric answer retained authority or lost the original proof status")
	}
	raw, err := json.Marshal(sample)
	if err != nil || s.containsPrivateValue(raw) || len(sample.ParsedNumbers) != 0 || len(sample.Features) != 0 {
		t.Fatal("numeric credential survived in usable sample data", err)
	}
}

func TestPrivacyReviewErrorPathsSanitizeMetadata(t *testing.T) {
	for _, test := range []struct{ name, reason string }{
		{"http_error", "HTTP_429"},
		{"invalid_envelope", "INVALID_RESPONSE_ENVELOPE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.name == "http_error" {
					w.WriteHeader(http.StatusTooManyRequests)
				} else {
					w.Write([]byte(`{"choices":[]}`))
				}
			}))
			target := addTestTarget(t, s)
			batch := modelstore.Batch{ID: "privacy-error", TargetID: target.ID, Status: "PENDING", StartedAt: nowText()}
			if err := s.store.PutBatch(batch); err != nil {
				t.Fatal(err)
			}
			target.ModelID = "echo-" + hex.EncodeToString(s.reviewKey)
			sample, reason := s.probe(context.Background(), target, batch.ID, collectionChallenges()[0], 0, 3)
			if reason != test.reason || sample.RouteEvidence["original_invalid_reason"] != test.reason || sample.Valid {
				t.Error("metadata redaction erased the original request failure")
			}
			if err := s.store.PutSample(sample); err != nil {
				t.Fatal(err)
			}
			raw, err := s.store.Export()
			if err != nil || s.containsPrivateValue(raw) {
				t.Fatal("failed probe persisted a reflected credential", err)
			}
		})
	}
}

func TestPrivacyReviewCleanSampleRetainsOriginalProof(t *testing.T) {
	s := testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { syntheticReply(w, r, true) }))
	// The public default must not match the static magpie_controlled_challenge tag.
	s.config.GatewayKey = "magpie"
	target := addTestTarget(t, s)
	id, err := s.StartCheck(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := waitBatch(t, s, id)
	samples, err := s.store.Samples(id)
	if err != nil || len(samples) != 1 {
		t.Fatal("clean sample missing", err)
	}
	if !samples[0].Valid || samples[0].RouteEvidence["authenticated"] != true || samples[0].RouteEvidence["protocol_verified"] != false {
		t.Fatal("clean sample lost its actual route/protocol/parse status")
	}
	if result.IdentityStatus != "INCONCLUSIVE" || result.CalibratedProbability != nil {
		t.Fatal("clean uncalibrated sample acquired identity authority")
	}
}
