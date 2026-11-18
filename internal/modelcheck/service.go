// Package modelcheck collects independent challenges through the local Magpie
// gateway. It observes behavior; it never gates traffic or changes providers.
package modelcheck

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/koala9527/magpie-model-check/internal/fingerprint"
	"github.com/koala9527/magpie-model-check/internal/modelstore"
)

const ChallengeVersion = "modeltrace-challenge-v1"
const LegacyBankVersion = "modeltrace-6cd4a8ff-legacy"
const MaxOutputTokens = 2048

// Config is private boot configuration. It is sent by the parent on stdin,
// never in process arguments, page options, database settings or API exports.
type Config struct {
	GatewayURL    string `json:"gateway_url"`
	GatewayKey    string `json:"gateway_key"`
	RouteKey      string `json:"route_key"`
	LegacyDB      string `json:"legacy_db"`
	ProtocolGuard bool   `json:"protocol_guard"`
}
type job struct {
	id     string
	cancel context.CancelFunc
}
type Service struct {
	store      *modelstore.Store
	config     Config
	client     *http.Client
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	active     map[string]job
	wg         sync.WaitGroup
	closeOnce  sync.Once
	references map[string]*referenceEntry
	dataDir    string
	reviewKey  []byte
	audits     referenceAudits
}

func Secret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("secure random source unavailable")
	}
	return hex.EncodeToString(b)
}
func nowText() string  { return time.Now().UTC().Format(time.RFC3339Nano) }
func usageDay() string { return time.Now().UTC().Format("2006-01-02") }
func localGateway(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/" && u.Path != "/v1") {
		return "", errors.New("LOCAL_GATEWAY_REQUIRED")
	}
	if u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" {
		return "", errors.New("LOCAL_GATEWAY_REQUIRED")
	}
	if u.Hostname() == "localhost" {
		port := u.Port()
		u.Host = "127.0.0.1"
		if port != "" {
			u.Host += ":" + port
		}
	}
	return strings.TrimSuffix(strings.TrimRight(u.String(), "/"), "/v1"), nil
}
func New(dir string, cfg Config) (*Service, error) {
	if cfg.GatewayURL != "" {
		base, err := localGateway(cfg.GatewayURL)
		if err != nil {
			return nil, err
		}
		cfg.GatewayURL = base
	}
	store, err := modelstore.Open(dir, cfg.LegacyDB)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{store: store, config: cfg, ctx: ctx, cancel: cancel, active: map[string]job{}, references: map[string]*referenceEntry{}, client: &http.Client{Transport: &http.Transport{Proxy: nil, MaxIdleConns: 8, IdleConnTimeout: 30 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("REDIRECT_REFUSED") }}}
	for _, c := range collectionChallenges() {
		if err = store.PutTemplate(modelstore.Template{ID: c.ID, Name: c.ID, Version: c.Version, Prompt: c.Prompt, PromptSHA256: c.SHA256, ParserVersion: c.ParserVersion, Parameters: c.Parameters, CreatedAt: nowText()}); err != nil {
			s.Close()
			return nil, err
		}
	}
	// Refresh placeholder/modified records too; this pinned artifact is always
	// descriptive, including when an imported backup claims it is trusted.
	if err = s.refreshBuiltinReference(); err != nil {
		s.Close()
		return nil, err
	}
	// A crash leaves a persisted batch, not an automatically replayed paid call.
	if err = s.openReferenceAudits(dir); err != nil {
		s.Close()
		return nil, err
	}
	if err = s.rebuildReferences(); err != nil {
		s.Close()
		return nil, err
	}
	batches, err := store.Batches("", 10000)
	if err != nil {
		s.Close()
		return nil, err
	}
	for _, b := range batches {
		if b.CompletedAt == "" {
			b.Status = "UNAVAILABLE"
			b.CompletedAt = nowText()
			b.ErrorMessage = "SERVICE_RESTARTED"
			if err = s.persistResult(b, modelstore.Result{IdentityStatus: "UNAVAILABLE", RejectionReason: "SERVICE_RESTARTED"}); err != nil {
				s.Close()
				return nil, err
			}
		}
	}
	s.wg.Add(1)
	go s.schedule()
	return s, nil
}
func (s *Service) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.cancel()
		s.mu.Unlock()
		s.wg.Wait()
		s.client.CloseIdleConnections()
		s.store.Close()
	})
}
func (s *Service) ActiveJobs() []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]string, 0, len(s.active))
	for target, j := range s.active {
		out = append(out, map[string]string{"target_id": target, "batch_id": j.id})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["target_id"] < out[j]["target_id"] })
	return out
}
func (s *Service) StartCheck(targetID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return "", errors.New("SERVICE_STOPPED")
	}
	if _, exists := s.active[targetID]; exists {
		return "", errors.New("TARGET_BUSY")
	}
	target, err := s.store.Target(targetID)
	if err != nil {
		return "", err
	}
	if !target.Enabled {
		return "", errors.New("TARGET_DISABLED")
	}
	if until, e := time.Parse(time.RFC3339Nano, target.RetryAfter); e == nil && time.Now().Before(until) {
		return "", errors.New("RETRY_BACKOFF")
	}
	settings, err := s.store.Settings()
	if err != nil {
		return "", err
	}
	if len(s.active) >= settings.Concurrency {
		return "", errors.New("CONCURRENCY_LIMIT")
	}
	id := Secret()[:32]
	batch := modelstore.Batch{ID: id, TargetID: targetID, ClaimedModel: target.ClaimedModel, AlgorithmVersion: fingerprint.AlgorithmVersion, ChallengeVersion: ChallengeVersion, BankVersion: LegacyBankVersion, Status: "PENDING", RouteBindingStatus: "UNVERIFIED", StartedAt: nowText()}
	reference := s.activeReference()
	if reference != nil {
		batch.BankVersion = reference.report.BankVersion
	}
	if err = s.store.PutBatch(batch); err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.active[targetID] = job{id: id, cancel: cancel}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		s.collect(ctx, target, batch, settings, reference)
		s.mu.Lock()
		delete(s.active, targetID)
		s.mu.Unlock()
	}()
	return id, nil
}
func (s *Service) CancelCheck(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.active {
		if j.id == id {
			j.cancel()
			return true
		}
	}
	return false
}
func due(target modelstore.Target, at time.Time) bool {
	if !target.Enabled || !target.AutoCheck || target.ProviderID == "" {
		return false
	}
	if retry, err := time.Parse(time.RFC3339Nano, target.RetryAfter); err == nil && at.Before(retry) {
		return false
	}
	last, err := time.Parse(time.RFC3339Nano, target.LastCheckedAt)
	if err != nil {
		return true
	}
	minutes := target.IntervalMinutes
	if minutes < 15 {
		minutes = 15
	}
	return !at.Before(last.Add(time.Duration(minutes) * time.Minute))
}
func (s *Service) schedule() {
	defer s.wg.Done()
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	lastPrune := time.Now()
	for {
		select {
		case <-s.ctx.Done():
			return
		case at := <-timer.C:
			targets, err := s.store.Targets()
			if err != nil {
				continue
			}
			for _, t := range targets {
				if due(t, at) {
					s.StartCheck(t.ID)
				}
			}
			if at.Sub(lastPrune) > time.Hour {
				settings, err := s.store.Settings()
				if err == nil {
					s.store.Prune(settings.RetentionDays)
				}
				lastPrune = at
			}
		}
	}
}

func (s *Service) collect(ctx context.Context, target modelstore.Target, batch modelstore.Batch, settings modelstore.Settings, reference *referenceEntry) {
	result := modelstore.Result{IdentityStatus: "INCONCLUSIVE", RejectionReason: "NO_VALIDATED_REFERENCE_OR_CALIBRATION", Candidates: []map[string]any{}, Metrics: map[string]any{"calibration_available": false, "bank_trusted": false}}
	result.Metrics["claim_snapshot"] = map[string]any{"provider_id": target.ProviderID, "model_id": target.ModelID, "claimed_model": target.ClaimedModel, "expected_family": target.ExpectedFamily, "expected_version": target.ExpectedVersion}
	result.Metrics["collection_protocol_sha256"] = collectionProtocolSHA256()
	result.Metrics["candidate_method"] = fingerprint.AlgorithmVersion
	fail := func(reason string) {
		result.IdentityStatus = "UNAVAILABLE"
		result.RejectionReason = reason
		batch.ErrorMessage = reason
	}
	if s.config.GatewayURL == "" {
		fail("GATEWAY_NOT_CONFIGURED")
		s.persistResult(batch, result)
		return
	}
	challenges := collectionChallenges()
	count := target.RequestBudget
	if count < 1 {
		count = 3
	}
	if count > 12 {
		count = 12
	}
	// Budget is a ceiling. Above the partial 1–2 request case, plan only whole
	// protocol rounds rather than silently treating duplicate challenges as one.
	if count >= len(challenges) {
		count -= count % len(challenges)
	}
	reserved := 0
	for i := 0; i < count; i++ {
		reserved += len([]byte(challenges[i%len(challenges)].Prompt)) + MaxOutputTokens + 4096
	}
	day := usageDay()
	if err := s.store.Reserve(day, count, reserved); err != nil {
		fail("BUDGET_EXHAUSTED")
		s.persistResult(batch, result)
		return
	}
	allNumbers := []int{}
	verified := true
	topCounts := map[string]int{}
	actualTokens := 0
	samples := []modelstore.Sample{}
	for i := 0; i < count; i++ {
		if ctx.Err() != nil {
			fail("CANCELLED")
			break
		}
		c := challenges[i%len(challenges)]
		sample, reason := s.probe(ctx, target, batch.ID, c, i, settings.TimeoutSeconds)
		if sample.ID == "" {
			fail(reason)
			break
		}
		actualTokens += sample.InputTokens + sample.OutputTokens
		if sample.RouteEvidence["authenticated"] != true {
			verified = false
		}
		if err := s.store.PutSample(sample); err != nil {
			fail("STORAGE_FAILED")
			break
		}
		samples = append(samples, sample)
		if reason != "" {
			fail(reason)
			break
		}
		if sample.Valid {
			batch.ValidSamples++
			allNumbers = append(allNumbers, sample.ParsedNumbers...)
			if candidates, err := fingerprint.Rank(sample.ParsedNumbers); err == nil && len(candidates) > 0 {
				topCounts[candidates[0].Model]++
			}
		}
	}
	result.Metrics["reserved_tokens"] = reserved
	result.Metrics["observed_tokens"] = actualTokens
	if actualTokens > reserved {
		if s.store.ChargeExtra(day, actualTokens-reserved) != nil {
			fail("USAGE_STORAGE_FAILED")
		}
	}
	result.Metrics["requested_samples"] = count
	result.Metrics["valid_samples"] = batch.ValidSamples
	result.Metrics["top_candidate_counts"] = topCounts
	if verified && len(samples) == count {
		batch.RouteBindingStatus = "VERIFIED"
	} else {
		batch.RouteBindingStatus = "UNVERIFIED"
	}
	if result.IdentityStatus != "UNAVAILABLE" {
		if !verified {
			result.IdentityStatus = "ROUTE_UNVERIFIED"
			result.RejectionReason = "MISSING_OR_MISMATCHED_AUTHENTICATED_ROUTE"
		} else if batch.ValidSamples == 0 {
			result.RejectionReason = "NO_VALID_SAMPLES"
		}
		if len(allNumbers) > 0 {
			candidates, err := fingerprint.Rank(allNumbers)
			if err == nil {
				for _, c := range candidates {
					result.Candidates = append(result.Candidates, map[string]any{"model": c.Model, "family": c.Family, "score": c.Score, "distance": c.Distance})
				}
				if len(candidates) > 0 {
					result.Top1Score = &candidates[0].Score
				}
				if len(candidates) > 1 {
					result.Top2Score = &candidates[1].Score
					margin := candidates[0].Score - candidates[1].Score
					result.ScoreMargin = &margin
				}
			}
		}
	}
	applyReferenceDecision(&result, target, samples, reference)
	s.persistResult(batch, result)
}
func (s *Service) persistResult(batch modelstore.Batch, result modelstore.Result) error {
	batch.Status = result.IdentityStatus
	batch.CompletedAt = nowText()
	result.ID = Secret()[:32]
	result.BatchID = batch.ID
	result.ExpectedModel = batch.ClaimedModel
	result.BankVersion = batch.BankVersion
	result.AlgorithmVersion = batch.AlgorithmVersion
	result.CreatedAt = batch.CompletedAt
	if err := s.store.PutResult(result); err != nil {
		return err
	}
	if err := s.store.PutBatch(batch); err != nil {
		return err
	}
	target, err := s.store.Target(batch.TargetID)
	if err != nil {
		return err
	}
	target.LastStatus = batch.Status
	target.LastCheckedAt = batch.CompletedAt
	if result.IdentityStatus == "UNAVAILABLE" {
		target.RetryAfter = time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339Nano)
	} else {
		target.RetryAfter = ""
	}
	_, err = s.store.PutTarget(target)
	return err
}

// redactSample covers every serialized sample field, including nested metadata
// and JSON carried inside strings. Verification and parsing precede this boundary.
func containsSampleCredentialJSON(raw string, secrets []string, depth int) bool {
	if depth > 64 {
		return true
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	nesting := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return false
		}
		if err != nil {
			return true
		}
		switch value := token.(type) {
		case json.Delim:
			switch value {
			case '{', '[':
				nesting++
				if nesting > 64 {
					return true
				}
			case '}', ']':
				nesting--
			}
		case string:
			for _, secret := range secrets {
				if secret != "" && strings.Contains(value, secret) {
					return true
				}
			}
			trimmed := strings.TrimSpace(value)
			if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
				if containsSampleCredentialJSON(value, secrets, depth+1) {
					return true
				}
			}
		}
	}
}

func (s *Service) redactSample(sample *modelstore.Sample) error {
	secrets := []string{s.config.GatewayKey, s.config.RouteKey, hex.EncodeToString(s.reviewKey)}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	var scrub func(any, int) (any, bool)
	scrub = func(value any, depth int) (any, bool) {
		if depth > 64 {
			return "[REDACTED_CREDENTIAL]", true
		}
		changed := false
		switch v := value.(type) {
		case map[string]any:
			clean := make(map[string]any, len(v))
			for key, item := range v {
				cleanKey, keyChanged := scrub(key, depth+1)
				cleanItem, itemChanged := scrub(item, depth+1)
				clean[cleanKey.(string)] = cleanItem
				changed = changed || keyChanged || itemChanged
			}
			return clean, changed
		case []any:
			for i, item := range v {
				clean, itemChanged := scrub(item, depth+1)
				v[i] = clean
				changed = changed || itemChanged
			}
			return v, changed
		case string:
			trimmed := strings.TrimSpace(v)
			if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
				if containsSampleCredentialJSON(v, secrets, 0) {
					return "[REDACTED_CREDENTIAL]", true
				}
				decoder := json.NewDecoder(strings.NewReader(v))
				decoder.UseNumber()
				var nested, extra any
				if decoder.Decode(&nested) == nil && decoder.Decode(&extra) == io.EOF {
					if clean, nestedChanged := scrub(nested, depth+1); nestedChanged {
						raw, _ := json.Marshal(clean)
						v, changed = string(raw), true
					}
				}
			}
			for _, secret := range secrets {
				if secret != "" && strings.Contains(v, secret) {
					v, changed = strings.ReplaceAll(v, secret, "[REDACTED_CREDENTIAL]"), true
				}
			}
			return v, changed
		}
		return value, false
	}
	raw, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		return err
	}
	// This fixed collector label contains the public default gateway password
	// "magpie"; it is not response data or a reflected credential.
	source := fields["source_type"]
	if source == "magpie_controlled_challenge" {
		delete(fields, "source_type")
	}
	clean, changed := scrub(fields, 0)
	if !changed {
		return nil
	}
	if source == "magpie_controlled_challenge" {
		clean.(map[string]any)["source_type"] = source
	}
	raw, err = json.Marshal(clean)
	if err != nil {
		return err
	}
	var sanitized modelstore.Sample
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&sanitized); err != nil {
		return err
	}
	if sanitized.RouteEvidence == nil {
		sanitized.RouteEvidence = map[string]any{}
	}
	sanitized.RouteEvidence["original_response_authenticated"] = sample.RouteEvidence["authenticated"] == true
	sanitized.RouteEvidence["original_protocol_verified"] = sample.RouteEvidence["protocol_verified"] == true
	sanitized.RouteEvidence["original_parse_valid"] = sample.Valid
	sanitized.RouteEvidence["original_invalid_reason"] = sanitized.InvalidReason
	sanitized.RouteEvidence["authenticated"] = false
	sanitized.RouteEvidence["protocol_verified"] = false
	sanitized.RouteEvidence["redacted"] = true
	sanitized.Valid, sanitized.InvalidReason = false, "REDACTED_CREDENTIAL_IN_SAMPLE"
	sanitized.ParsedNumbers, sanitized.Features = nil, nil
	sanitized.ResponseSHA256 = digestBytes([]byte(sanitized.RawResponse))
	*sample = sanitized
	return nil
}

func (s *Service) probe(parent context.Context, target modelstore.Target, batchID string, c fingerprint.Challenge, index, timeout int) (sample modelstore.Sample, reason string) {
	id := Secret()[:32]
	sample = modelstore.Sample{ID: id, TargetID: target.ID, BatchID: batchID, SourceType: "magpie_controlled_challenge", ChallengeID: c.ID, ChallengeIndex: index, ParserVersion: c.ParserVersion, CollectedAt: nowText(), RouteEvidence: map[string]any{"authenticated": false}}
	defer func() {
		if s.redactSample(&sample) != nil {
			sample, reason = modelstore.Sample{}, "SAMPLE_PRIVACY_REJECTED"
		}
	}()
	body := map[string]any{"model": target.ModelID, "stream": false, "max_tokens": MaxOutputTokens, "messages": []map[string]string{{"role": "user", "content": c.Prompt}}, "_magpie_model_check_probe": map[string]any{"version": 1, "id": id}}
	for k, v := range c.Parameters {
		if k != "model" && k != "messages" && k != "stream" {
			body[k] = v
		}
	}
	sample.RequestParams = map[string]any{"model": target.ModelID, "stream": false, "max_tokens": MaxOutputTokens, "prompt_sha256": c.SHA256, "challenge_version": c.Version, "min": c.Min, "max": c.Max, "count": c.Count, "parameters": c.Parameters}
	sample.SystemPromptHash = emptySystemPromptHash()
	encoded, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
	defer cancel()
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.config.GatewayURL+"/v1/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		sample.InvalidReason = "REQUEST_FAILED"
		return sample, sample.InvalidReason
	}
	req.Header.Set("Content-Type", "application/json")
	if s.config.GatewayKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.config.GatewayKey)
	}
	response, err := s.client.Do(req)
	sample.LatencyMS = int(time.Since(start).Milliseconds())
	if err != nil {
		sample.InvalidReason = "REQUEST_FAILED"
		if parent.Err() != nil {
			sample.InvalidReason = "CANCELLED"
		} else if ctx.Err() != nil {
			sample.InvalidReason = "TIMEOUT"
		}
		return sample, sample.InvalidReason
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		sample.InvalidReason = "HTTP_" + strconv.Itoa(response.StatusCode)
		return sample, sample.InvalidReason
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil || len(raw) > 2*1024*1024 {
		sample.InvalidReason = "RESPONSE_TOO_LARGE_OR_INTERRUPTED"
		return sample, sample.InvalidReason
	}
	var reply struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Route RouteEvidence `json:"_magpie_model_check_route"`
		Usage struct {
			Input  int `json:"prompt_tokens"`
			Output int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &reply) != nil || len(reply.Choices) != 1 {
		sample.InvalidReason = "INVALID_RESPONSE_ENVELOPE"
		return sample, sample.InvalidReason
	}
	sample.RawResponse = reply.Choices[0].Message.Content
	sample.ResponseModelField = reply.Model
	sample.InputTokens = reply.Usage.Input
	sample.OutputTokens = reply.Usage.Output
	if sample.InputTokens < 0 {
		sample.InputTokens = 0
	}
	if sample.OutputTokens < 0 {
		sample.OutputTokens = 0
	}
	auth := verifyRoute(reply.Route, s.config.RouteKey, id, target.ModelID, target.ProviderID, sample.RawResponse, reply.Choices[0].FinishReason)
	if reply.Route.Version == 3 {
		auth = auth && verifyResponseEnvelope(reply.Route, raw)
	}
	sample.RouteEvidence = reply.Route.Public(auth)
	sample.RouteEvidence["protocol_verified"] = verifiedProtocol(reply.Route, auth, s.config.ProtocolGuard, body)
	sample.RouteEvidence["expected_request_sha256"] = requestBodySHA256(body)
	sample.RequestParams["finish_reason"] = reply.Choices[0].FinishReason
	sample.RequestID = id
	// Both checks describe the original reply; the deferred privacy boundary
	// invalidates sanitized evidence without reinterpreting the modified answer.
	numbers, invalid := fingerprint.Parse(sample.RawResponse, c, reply.Choices[0].FinishReason)
	sample.ParsedNumbers = numbers
	sample.InvalidReason = invalid
	sample.Valid = invalid == ""
	if sample.Valid {
		sample.Features, _ = fingerprint.Features(numbers)
	}
	return sample, ""
}

func (s *Service) Models(ctx context.Context) ([]map[string]string, error) {
	if s.config.GatewayURL == "" {
		return []map[string]string{}, errors.New("GATEWAY_NOT_CONFIGURED")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", s.config.GatewayURL+"/v1/models", nil)
	if s.config.GatewayKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.config.GatewayKey)
	}
	r, err := s.client.Do(req)
	if err != nil {
		return nil, errors.New("GATEWAY_UNAVAILABLE")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP_%d", r.StatusCode)
	}
	var envelope struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 1024*1024)).Decode(&envelope) != nil {
		return nil, errors.New("INVALID_MODEL_LIST")
	}
	out := make([]map[string]string, 0, len(envelope.Data))
	for _, m := range envelope.Data {
		if m.ID != "" && len(m.ID) < 256 && !strings.ContainsAny(m.ID, "\r\n\x00") {
			out = append(out, map[string]string{"id": m.ID})
		}
	}
	return out, nil
}
