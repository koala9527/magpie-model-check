package modelcheck

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/koala9527/magpie-model-check/internal/attribution"
	"github.com/koala9527/magpie-model-check/internal/fingerprint"
	"github.com/koala9527/magpie-model-check/internal/modelstore"
)

//go:embed web/index.html web/app.js web/app.css web/ui-format.mjs web/ui-forms.mjs web/ui-refresh.mjs web/ui-reference.mjs
var web embed.FS

// Raw JSON is carried as a string. json.RawMessage would be compacted/escaped
// by the outer encoder and would no longer match the stored byte digest.
type bankTransfer struct {
	modelstore.Bank
	Data string `json:"data"`
}

func exportBank(bank modelstore.Bank) bankTransfer {
	data := string(bank.Data)
	bank.Data = nil
	return bankTransfer{Bank: bank, Data: data}
}

func apiJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, status int, code, message string) {
	apiJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func decodeAPI(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		apiError(w, 422, "INVALID_INPUT", "请检查 JSON 内容和字段")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		apiError(w, 422, "INVALID_INPUT", "请求只接受一个 JSON 对象")
		return false
	}
	return true
}
func bearer(r *http.Request, token string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
}

func (s *Service) Handler(controlToken, pageToken, origin string) http.Handler {
	mux := http.NewServeMux()
	parsed, _ := url.Parse(origin)
	allowedHost := parsed.Host
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		body, err := web.ReadFile("web/index.html")
		if err != nil {
			apiError(w, 500, "PAGE_UNAVAILABLE", "页面资源尚未准备好")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(strings.ReplaceAll(string(body), "{{PAGE_TOKEN}}", html.EscapeString(pageToken))))
	})
	assets, _ := fs.Sub(web, "web")
	for _, name := range []string{"app.js", "app.css", "ui-format.mjs", "ui-forms.mjs", "ui-refresh.mjs", "ui-reference.mjs"} {
		mux.Handle("GET /"+name, http.FileServer(http.FS(assets)))
	}
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		targets, e1 := s.store.Targets()
		batches, e2 := s.store.Batches("", 200)
		banks, e3 := s.store.Banks()
		settings, e4 := s.store.Settings()
		usage, e5 := s.store.Usage(usageDay())
		legacy, e6 := s.store.Legacy()
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || e6 != nil {
			apiError(w, 503, "STORAGE_FAILED", "本地数据读取失败，请重启后重试")
			return
		}
		// Bank payloads and any credentials are omitted from the list view.
		s.mu.Lock()
		activeReference := s.activeReference()
		activeBankID, calibration := "", "NOT_VALIDATED"
		if activeReference != nil {
			activeBankID, calibration = s.audits.ActiveBankID, "VALIDATED_FOR_REVIEWED_CHANNELS_AND_DATES"
		}
		for i := range banks {
			banks[i] = referenceMetadata(banks[i], s.references[banks[i].ID])
			banks[i].ValidationMetrics["active"] = banks[i].ID == activeBankID
			banks[i].Data = nil
		}
		s.mu.Unlock()
		apiJSON(w, 200, map[string]any{"targets": targets, "batches": batches, "banks": banks, "settings": settings, "usage": usage, "active_jobs": s.ActiveJobs(), "legacy_records": legacy,
			"reference":    map[string]any{"active_bank_id": activeBankID, "collection_protocol_sha256": collectionProtocolSHA256()},
			"capabilities": map[string]any{"ordinary_requests": "PASS_THROUGH", "gateway_configured": s.config.GatewayURL != "", "algorithm_version": fingerprint.AlgorithmVersion, "challenge_version": ChallengeVersion, "identity_calibration": calibration, "probability_available": false, "schedule_requires_provider": true}})
	})
	mux.HandleFunc("GET /api/models", func(w http.ResponseWriter, r *http.Request) {
		models, err := s.Models(r.Context())
		if err != nil {
			apiError(w, 503, "GATEWAY_UNAVAILABLE", "Magpie 模型列表暂未读取；可填写完整模型 ID")
			return
		}
		apiJSON(w, 200, map[string]any{"models": models, "gateway_configured": true})
	})
	mux.HandleFunc("POST /api/targets", func(w http.ResponseWriter, r *http.Request) { s.saveTarget(w, r, true) })
	mux.HandleFunc("PUT /api/targets/{id}", func(w http.ResponseWriter, r *http.Request) { s.saveTarget(w, r, false) })
	mux.HandleFunc("DELETE /api/targets/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, busy := s.active[id]; busy {
			apiError(w, 409, "TARGET_BUSY", "请先取消这条目标的检测")
			return
		}
		if err := s.store.DeleteTarget(id); err != nil {
			apiError(w, 404, "NOT_FOUND", "目标记录不存在")
			return
		}
		apiJSON(w, 200, map[string]bool{"deleted": true})
	})
	mux.HandleFunc("POST /api/targets/{id}/checks", func(w http.ResponseWriter, r *http.Request) {
		var input struct{}
		if !decodeAPI(w, r, &input) {
			return
		}
		id, err := s.StartCheck(r.PathValue("id"))
		if err != nil {
			code := "CHECK_NOT_STARTED"
			if errors.Is(err, modelstore.ErrNotFound) {
				apiError(w, 404, "NOT_FOUND", "目标记录不存在")
				return
			}
			if err.Error() == "TARGET_BUSY" {
				code = "TARGET_BUSY"
			}
			apiError(w, 409, code, "检测尚未启动：请检查并发、退避时间和目标开关")
			return
		}
		apiJSON(w, 202, map[string]string{"batch_id": id})
	})
	mux.HandleFunc("GET /api/batches/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		batch, err := s.store.Batch(id)
		if err != nil {
			apiError(w, 404, "NOT_FOUND", "检测记录不存在")
			return
		}
		samples, err := s.store.Samples(id)
		if err != nil {
			apiError(w, 503, "STORAGE_FAILED", "样本读取失败")
			return
		}
		var result any
		stored, err := s.store.Result(id)
		if err == nil {
			result = stored
		} else if !errors.Is(err, modelstore.ErrNotFound) {
			apiError(w, 503, "STORAGE_FAILED", "报告读取失败")
			return
		}
		apiJSON(w, 200, map[string]any{"batch": batch, "samples": samples, "result": result})
	})
	mux.HandleFunc("POST /api/batches/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		var input struct{}
		if !decodeAPI(w, r, &input) {
			return
		}
		apiJSON(w, 200, map[string]bool{"cancelled": s.CancelCheck(r.PathValue("id"))})
	})
	mux.HandleFunc("POST /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var input modelstore.Settings
		if !decodeAPI(w, r, &input) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.store.PutSettings(input) != nil {
			apiError(w, 422, "INVALID_SETTINGS", "请检查预算、并发、超时和保留天数的范围")
			return
		}
		apiJSON(w, 200, input)
	})
	mux.HandleFunc("GET /api/backup", func(w http.ResponseWriter, r *http.Request) {
		data, err := s.store.Export()
		if err != nil {
			apiError(w, 503, "BACKUP_FAILED", "备份生成失败")
			return
		}
		if s.transferHasCredentials(data) {
			apiError(w, 503, "EXPORT_PRIVACY_CHECK_FAILED", "本地资料未通过凭据检查，请清理参考文件后再生成备份")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="magpie-model-check-backup.json"`)
		w.Write(data)
	})
	mux.HandleFunc("POST /api/restore", func(w http.ResponseWriter, r *http.Request) {
		if len(s.ActiveJobs()) > 0 {
			apiError(w, 409, "CHECKS_RUNNING", "请取消正在执行的检测后再恢复")
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32*1024*1024))
		if err != nil {
			apiError(w, 422, "INVALID_BACKUP", "备份格式、版本或完整性校验未通过")
			return
		}
		if s.transferHasCredentials(data) {
			apiError(w, 422, "INVALID_BACKUP", "备份包含凭据或未通过嵌套内容检查")
			return
		}
		// Read the body without holding the service lock, then recheck and hold
		// it across the complete transaction. A new job cannot slip between.
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.active) > 0 {
			apiError(w, 409, "CHECKS_RUNNING", "请取消正在执行的检测后再恢复")
			return
		}
		// A restore transaction does not carry local source audit authority.
		// Invalidate it durably before replacing data, even if deeper validation
		// later rejects this restore. Existing evidence remains exportable.
		if s.clearReferenceAudits() != nil {
			apiError(w, 503, "LOCAL_REVIEW_NOT_SAVED", "本机审核状态重置失败，尚未开始恢复数据")
			return
		}
		if s.store.Restore(data) != nil {
			apiError(w, 422, "INVALID_BACKUP", "备份格式、版本或完整性校验未通过")
			return
		}
		if s.refreshBuiltinReference() != nil || s.rebuildReferences() != nil {
			apiError(w, 503, "REFERENCE_REFRESH_FAILED", "数据已恢复，参考库仍需重新打开并审核")
			return
		}
		apiJSON(w, 200, map[string]bool{"restored": true})
	})
	mux.HandleFunc("POST /api/banks", func(w http.ResponseWriter, r *http.Request) {
		var transfer bankTransfer
		if !decodeAPI(w, r, &transfer) {
			return
		}
		input := transfer.Bank
		input.Data = json.RawMessage(transfer.Data)
		rawImport, _ := json.Marshal(transfer)
		findings, scanErr := attribution.CredentialFindingsJSON(rawImport)
		if scanErr != nil || len(findings) != 0 || s.containsPrivateValue(rawImport) {
			apiError(w, 422, "CREDENTIAL_IN_REFERENCE", "参考资料包含凭据字段或未通过嵌套内容检查，请先去除密钥")
			return
		}
		if input.ID == "builtin-modeltrace" || input.AlgorithmVersion != fingerprint.AlgorithmVersion || input.ChallengeVersion != ChallengeVersion || input.Version == "" || !json.Valid(input.Data) || len(input.Provenance) > 4000 || len(input.Provenance) == 0 {
			apiError(w, 422, "INCOMPATIBLE_BANK", "请填写同协议的算法、题目版本和样本来源")
			return
		}
		input.Trusted = false
		input.ValidationMetrics = map[string]any{"independent_validation": false, "review_status": "PENDING_SOURCE_REVIEW"}
		if input.ID == "" {
			input.ID = Secret()[:32]
		}
		input.CreatedAt = nowText()
		digest := sha256.Sum256(input.Data)
		actual := hex.EncodeToString(digest[:])
		if input.SHA256 != "" && input.SHA256 != actual {
			apiError(w, 422, "BANK_HASH_MISMATCH", "参考库内容与 SHA-256 记录不一致")
			return
		}
		input.SHA256 = actual
		entry, err := loadReference(input, attribution.Options{})
		if err != nil {
			apiError(w, 422, "INVALID_REFERENCE_EXPERIMENT", "原始样本、哈希、分组或题目协议未通过重新计算")
			return
		}
		input = referenceMetadata(input, entry)
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.active) > 0 {
			apiError(w, 409, "CHECKS_RUNNING", "请先完成或取消检测再更新参考库")
			return
		}
		if s.forgetReference(input.ID) != nil {
			apiError(w, 503, "LOCAL_REVIEW_NOT_SAVED", "旧审核状态重置失败，参考文件尚未替换")
			return
		}
		if s.store.PutBank(input) != nil {
			apiError(w, 422, "INVALID_BANK", "参考库内容校验未通过")
			return
		}
		s.references[input.ID] = entry
		apiJSON(w, 201, exportBank(input))
	})
	mux.HandleFunc("GET /api/banks/{id}", s.inspectReference)
	mux.HandleFunc("POST /api/banks/{id}/review", s.reviewReference)
	mux.HandleFunc("POST /api/banks/{id}/activate", s.activateReference)
	mux.HandleFunc("POST /api/reference/reset", func(w http.ResponseWriter, r *http.Request) {
		var input struct{}
		if !decodeAPI(w, r, &input) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.active) > 0 {
			apiError(w, 409, "CHECKS_RUNNING", "请先完成或取消检测再切换参考库")
			return
		}
		next := s.copyAudits()
		next.ActiveBankID, next.ActiveSHA256 = "", ""
		if s.writeReferenceAudits(next) != nil {
			apiError(w, 503, "LOCAL_REVIEW_NOT_SAVED", "参考库选择保存失败")
			return
		}
		apiJSON(w, 200, map[string]bool{"reset": true})
	})
	mux.HandleFunc("GET /api/banks/{id}/export", func(w http.ResponseWriter, r *http.Request) {
		bank, err := s.store.Bank(r.PathValue("id"))
		if err != nil {
			apiError(w, 404, "NOT_FOUND", "参考库不存在")
			return
		}
		s.mu.Lock()
		bank = referenceMetadata(bank, s.references[bank.ID])
		s.mu.Unlock()
		data, _ := json.Marshal(exportBank(bank))
		if s.transferHasCredentials(data) {
			apiError(w, 503, "EXPORT_PRIVACY_CHECK_FAILED", "参考资料未通过凭据检查")
			return
		}
		apiJSON(w, 200, exportBank(bank))
	})
	mux.HandleFunc("DELETE /api/banks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "builtin-modeltrace" {
			apiError(w, 409, "BUILTIN_BANK", "内置历史资料随插件保留")
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.active) > 0 {
			apiError(w, 409, "CHECKS_RUNNING", "请先完成或取消检测再删除参考库")
			return
		}
		if s.forgetReference(r.PathValue("id")) != nil {
			apiError(w, 503, "LOCAL_REVIEW_NOT_SAVED", "本机审核状态保存失败")
			return
		}
		if s.store.DeleteBank(r.PathValue("id")) != nil {
			apiError(w, 404, "NOT_FOUND", "参考库不存在")
			return
		}
		delete(s.references, r.PathValue("id"))
		apiJSON(w, 200, map[string]bool{"deleted": true})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self' "+origin+"; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self' http://127.0.0.1:* http://localhost:*")
		if r.Host != allowedHost {
			apiError(w, 403, "INVALID_HOST", "访问地址未通过本机校验")
			return
		}
		requestOrigin := r.Header.Get("Origin")
		if requestOrigin != "" && requestOrigin != "null" && requestOrigin != origin {
			apiError(w, 403, "FOREIGN_ORIGIN", "请求来源未通过校验")
			return
		}
		isAPI := strings.HasPrefix(r.URL.Path, "/api/")
		if r.Method == http.MethodOptions && isAPI {
			w.Header().Set("Access-Control-Allow-Origin", "null")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type")
			w.WriteHeader(204)
			return
		}
		if isAPI {
			if !bearer(r, pageToken) && !bearer(r, controlToken) {
				apiError(w, 401, "AUTH_REQUIRED", "页面会话已结束，请重新打开插件页面")
				return
			}
			if requestOrigin == "null" {
				w.Header().Set("Access-Control-Allow-Origin", "null")
			}
		}
		if r.URL.Path == "/app.js" || r.URL.Path == "/ui-format.mjs" || r.URL.Path == "/ui-forms.mjs" || r.URL.Path == "/ui-refresh.mjs" || r.URL.Path == "/ui-reference.mjs" {
			w.Header().Set("Access-Control-Allow-Origin", "null")
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Service) saveTarget(w http.ResponseWriter, r *http.Request, create bool) {
	var input modelstore.Target
	if !decodeAPI(w, r, &input) {
		return
	}
	if input.ModelID == "" || input.ClaimedModel == "" || len(input.ModelID) > 256 || len(input.ClaimedModel) > 256 || len(input.ProviderID) > 256 || strings.ContainsAny(input.ModelID+input.ProviderID, "\r\n\x00") {
		apiError(w, 422, "INVALID_TARGET", "请填写完整模型 ID、声明身份和渠道 ID")
		return
	}
	if input.IntervalMinutes == 0 {
		input.IntervalMinutes = 60
	}
	if input.RequestBudget == 0 {
		input.RequestBudget = 3
	}
	if input.IntervalMinutes < 15 || input.IntervalMinutes > 43200 || input.RequestBudget < 1 || input.RequestBudget > 12 || (input.AutoCheck && input.ProviderID == "") {
		apiError(w, 422, "INVALID_TARGET", "定时检测需要渠道 ID；间隔至少 15 分钟，每次 1–12 个请求")
		return
	}
	// The previous record, in-flight check and write are one critical section,
	// using exactly the mutex StartCheck uses to reserve an active target.
	s.mu.Lock()
	defer s.mu.Unlock()
	if create {
		input.ID = ""
		input.LastStatus = "PENDING"
		input.LastCheckedAt = ""
		input.RetryAfter = ""
		input.CreatedAt = ""
		input.UpdatedAt = ""
	} else {
		previous, err := s.store.Target(r.PathValue("id"))
		if err != nil {
			apiError(w, 404, "NOT_FOUND", "目标记录不存在")
			return
		}
		if _, busy := s.active[previous.ID]; busy {
			apiError(w, 409, "TARGET_BUSY", "请先完成或取消目标检测再修改")
			return
		}
		input.ID = previous.ID
		input.LastStatus = previous.LastStatus
		input.LastCheckedAt = previous.LastCheckedAt
		input.RetryAfter = previous.RetryAfter
		input.CreatedAt = previous.CreatedAt
		if input.ModelID != previous.ModelID || input.ProviderID != previous.ProviderID || input.ClaimedModel != previous.ClaimedModel || input.ExpectedFamily != previous.ExpectedFamily || input.ExpectedVersion != previous.ExpectedVersion {
			input.LastStatus = "PENDING"
			input.LastCheckedAt = ""
			input.RetryAfter = ""
		}
	}
	target, err := s.store.PutTarget(input)
	if err != nil {
		apiError(w, 422, "INVALID_TARGET", "目标配置校验未通过")
		return
	}
	status := 200
	if create {
		status = 201
	}
	apiJSON(w, status, target)
}
