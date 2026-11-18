package modelcheck

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

// RouteEvidence is generated inside the local gateway after its last provider
// attempt. A model field, nonce or public source label on its own proves nothing.
type RouteEvidence struct {
	Version        int    `json:"version"`
	ProbeID        string `json:"probe_id"`
	TargetModelID  string `json:"target_model_id"`
	ProviderID     string `json:"provider_id"`
	ProviderModel  string `json:"provider_model"`
	AttemptCount   int    `json:"attempt_count"`
	Source         string `json:"source"`
	ResponseSHA256 string `json:"response_sha256"`
	RequestSHA256  string `json:"request_sha256,omitempty"`
	EnvelopeSHA256 string `json:"envelope_sha256,omitempty"`
	StreamSHA256   string `json:"stream_sha256,omitempty"`
	MAC            string `json:"mac"`
}

func (e RouteEvidence) payload() string {
	if e.Version == 3 {
		return strings.Join([]string{"model-check-route-v3", e.ProbeID, e.TargetModelID, e.ProviderID, e.ProviderModel, strconv.Itoa(e.AttemptCount), e.ResponseSHA256, e.RequestSHA256, e.EnvelopeSHA256, e.StreamSHA256}, "\n")
	}
	if e.Version == 2 {
		return strings.Join([]string{"model-check-route-v2", e.ProbeID, e.TargetModelID, e.ProviderID, e.ProviderModel, strconv.Itoa(e.AttemptCount), e.ResponseSHA256, e.RequestSHA256}, "\n")
	}
	return strings.Join([]string{"model-check-route-v1", e.ProbeID, e.TargetModelID, e.ProviderID, e.ProviderModel, strconv.Itoa(e.AttemptCount), e.ResponseSHA256}, "\n")
}

func verifyRoute(e RouteEvidence, key, nonce, target, expectedProvider, text, finish string) bool {
	if len(key) != 64 || (e.Version != 1 && e.Version != 2 && e.Version != 3) || len(nonce) != 32 || e.ProbeID != nonce || e.TargetModelID != target || expectedProvider == "" ||
		e.ProviderID != expectedProvider || e.ProviderModel == "" || e.AttemptCount < 1 || e.Source != "local-gateway-middleware" {
		return false
	}
	digest := sha256.Sum256([]byte(text + "\n" + finish))
	if e.ResponseSHA256 != hex.EncodeToString(digest[:]) {
		return false
	}
	got, err := hex.DecodeString(e.MAC)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(e.payload()))
	return hmac.Equal(got, mac.Sum(nil))
}

func (e RouteEvidence) Public(authenticated bool) map[string]any {
	if !authenticated {
		return map[string]any{"authenticated": false, "reason": "MISSING_OR_MISMATCHED_AUTHENTICATED_ROUTE"}
	}
	return map[string]any{"version": e.Version, "probe_id": e.ProbeID, "target_model_id": e.TargetModelID,
		"provider_id": e.ProviderID, "provider_model": e.ProviderModel, "attempt_count": e.AttemptCount,
		"source": e.Source, "response_sha256": e.ResponseSHA256, "request_sha256": e.RequestSHA256,
		"envelope_sha256": e.EnvelopeSHA256, "stream_sha256": e.StreamSHA256, "authenticated": authenticated}
}

func verifyResponseEnvelope(e RouteEvidence, raw []byte) bool {
	if e.Version != 3 || e.StreamSHA256 != "" || e.EnvelopeSHA256 == "" {
		return false
	}
	var body map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&body) != nil {
		return false
	}
	delete(body, "_magpie_model_check_route")
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(body) != nil {
		return false
	}
	// Go and ECMAScript both preserve this integer/string response contract.
	// A noncanonical extra number or Unicode escape declines proof rather than
	// accepting a partial digest of fields the collector later consumes.
	digest := sha256.Sum256(bytes.TrimSuffix(encoded.Bytes(), []byte("\n")))
	return e.EnvelopeSHA256 == hex.EncodeToString(digest[:])
}

func requestBodySHA256(body map[string]any) string {
	packet := make(map[string]any, len(body))
	for k, v := range body {
		if k != "model" && k != "_magpie_model_check_probe" {
			packet[k] = v
		}
	}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(packet) != nil {
		return ""
	}
	digest := sha256.Sum256(bytes.TrimSuffix(raw.Bytes(), []byte("\n")))
	return hex.EncodeToString(digest[:])
}

func verifiedProtocol(e RouteEvidence, authenticated, finalObserver bool, body map[string]any) bool {
	return authenticated && finalObserver && e.Version >= 2 && e.RequestSHA256 != "" && e.RequestSHA256 == requestBodySHA256(body)
}

func emptySystemPromptHash() string {
	digest := sha256.Sum256(nil)
	return hex.EncodeToString(digest[:])
}
