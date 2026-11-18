package modelcheck

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/koala9527/magpie-model-check/internal/attribution"
	"github.com/koala9527/magpie-model-check/internal/modelstore"
)

type sourceReview struct {
	BankSHA256                     string              `json:"bank_sha256"`
	CorpusSHA256                   string              `json:"corpus_sha256"`
	ProvenanceSHA256               string              `json:"provenance_sha256"`
	Reviewer                       string              `json:"reviewer"`
	ReviewNote                     string              `json:"review_note"`
	ApprovedRecordIDs              []string            `json:"approved_record_ids"`
	ConfirmIndependentSourceReview bool                `json:"confirm_independent_source_review"`
	Policy                         *attribution.Policy `json:"policy"`
	ReviewedAt                     string              `json:"reviewed_at,omitempty"`
}

type referenceAudits struct {
	Schema       int                     `json:"schema"`
	Reviews      map[string]sourceReview `json:"reviews"`
	ActiveBankID string                  `json:"active_bank_id"`
	ActiveSHA256 string                  `json:"active_sha256"`
}

type auditFile struct {
	State referenceAudits `json:"state"`
	MAC   string          `json:"mac"`
}

func (s *Service) auditMAC(state referenceAudits) string {
	raw, _ := json.Marshal(state)
	mac := hmac.New(sha256.New, s.reviewKey)
	mac.Write([]byte("magpie-local-source-review-v1\n"))
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

// This local key/seal is intentionally outside SQLite and every export. A
// backup's trusted/validated flags cannot re-create a source review on a new
// installation. The runtime has already secured the entire private data tree.
func (s *Service) openReferenceAudits(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	dir = abs
	s.dataDir = dir
	s.audits = referenceAudits{Schema: 1, Reviews: map[string]sourceReview{}}
	file := filepath.Join(dir, "reference-review.key")
	info, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		writer, createErr := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if createErr != nil {
			return createErr
		}
		_, err = writer.Write(key)
		if err == nil {
			err = writer.Sync()
		}
		closeErr := writer.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		s.reviewKey = key
	} else {
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != 32 {
			return errors.New("INVALID_LOCAL_REVIEW_KEY")
		}
		s.reviewKey, err = os.ReadFile(file)
		if err != nil || len(s.reviewKey) != 32 {
			return errors.New("INVALID_LOCAL_REVIEW_KEY")
		}
	}
	file = filepath.Join(dir, "reference-review.json")
	info, err = os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4<<20 {
		// Unreadable or tampered local audit data never upgrades imported flags.
		return nil
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var document auditFile
	if json.Unmarshal(raw, &document) != nil || document.State.Schema != 1 || document.State.Reviews == nil {
		return nil
	}
	want, decodeErr := hex.DecodeString(s.auditMAC(document.State))
	got, macErr := hex.DecodeString(document.MAC)
	if decodeErr == nil && macErr == nil && hmac.Equal(got, want) {
		s.audits = document.State
	}
	return nil
}

func (s *Service) writeReferenceAudits(next referenceAudits) error {
	file := filepath.Join(s.dataDir, "reference-review.json")
	if info, err := os.Lstat(file); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("INVALID_LOCAL_REVIEW_FILE")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, err := json.Marshal(auditFile{State: next, MAC: s.auditMAC(next)})
	if err != nil || len(raw) > 4<<20 {
		return errors.New("LOCAL_REVIEW_TOO_LARGE")
	}
	temp, err := os.CreateTemp(s.dataDir, ".reference-review-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	_, err = temp.Write(raw)
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, file); err != nil {
		return err
	}
	s.audits = next
	return nil
}

func (s *Service) copyAudits() referenceAudits {
	next := s.audits
	next.Reviews = make(map[string]sourceReview, len(s.audits.Reviews))
	for id, review := range s.audits.Reviews {
		next.Reviews[id] = review
	}
	return next
}

func auditOptions(bank modelstore.Bank, entry *referenceEntry, review sourceReview) attribution.Options {
	if !review.ConfirmIndependentSourceReview || review.Policy == nil || review.BankSHA256 != bank.SHA256 ||
		entry == nil || review.CorpusSHA256 != entry.report.CorpusSHA256 || review.ProvenanceSHA256 != digestBytes(entry.provenance) {
		return attribution.Options{}
	}
	approved := map[string]bool{}
	for _, id := range review.ApprovedRecordIDs {
		approved[id] = true
	}
	return attribution.Options{Policy: review.Policy, VerifyProvenance: func(binding attribution.Binding) error {
		if binding.CorpusSHA256 != review.CorpusSHA256 || !approved[binding.Record.ID] {
			return errors.New("RECORD_NOT_REVIEWED")
		}
		return nil
	}}
}

func (s *Service) rebuildReferences() error {
	banks, err := s.store.Banks()
	if err != nil {
		return err
	}
	s.references = map[string]*referenceEntry{}
	for _, bank := range banks {
		findings, scanErr := attribution.CredentialFindingsJSON(bank.Data)
		if scanErr != nil || len(findings) > 0 || s.containsPrivateValue(bank.Data) {
			continue
		}
		entry, loadErr := loadReference(bank, attribution.Options{})
		if loadErr != nil {
			continue
		}
		if review, found := s.audits.Reviews[bank.ID]; found {
			options := auditOptions(bank, entry, review)
			if options.VerifyProvenance != nil {
				if reviewed, err := loadReference(bank, options); err == nil {
					entry = reviewed
				}
			}
		}
		s.references[bank.ID] = entry
	}
	return nil
}

func (s *Service) reviewReference(w http.ResponseWriter, r *http.Request) {
	var input sourceReview
	if !decodeAPI(w, r, &input) {
		return
	}
	input.Reviewer, input.ReviewNote = strings.TrimSpace(input.Reviewer), strings.TrimSpace(input.ReviewNote)
	raw, _ := json.Marshal(input)
	findings, scanErr := attribution.CredentialFindingsJSON(raw)
	if !input.ConfirmIndependentSourceReview || input.Policy == nil || input.Reviewer == "" || len(input.Reviewer) > 256 ||
		input.ReviewNote == "" || len(input.ReviewNote) > 4000 || scanErr != nil || len(findings) > 0 || s.containsPrivateValue(raw) {
		apiError(w, 422, "INCOMPLETE_SOURCE_REVIEW", "请填写审核人、独立来源审核记录、实验目标，并确认逐条核对标签和渠道证据")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.active) > 0 {
		apiError(w, 409, "CHECKS_RUNNING", "请先完成或取消检测再审核参考库")
		return
	}
	bank, err := s.store.Bank(r.PathValue("id"))
	entry := s.references[bank.ID]
	if err != nil || entry == nil {
		apiError(w, 404, "NOT_FOUND", "参考库不存在或尚未通过原始数据检查")
		return
	}
	if entry.report.DatasetKind != attribution.Real {
		apiError(w, 409, "REFERENCE_NOT_REAL", "合成数据和历史中心只用于机制测试，真实身份审核需要独立采集记录")
		return
	}
	if input.BankSHA256 != bank.SHA256 || input.CorpusSHA256 != entry.report.CorpusSHA256 || input.ProvenanceSHA256 != digestBytes(entry.provenance) {
		apiError(w, 409, "REFERENCE_CHANGED", "参考文件或来源记录已变化，请重新打开审核内容")
		return
	}
	var bindings []attribution.Binding
	json.Unmarshal(entry.provenance, &bindings)
	approved := map[string]bool{}
	for _, id := range input.ApprovedRecordIDs {
		if approved[id] {
			apiError(w, 422, "INCOMPLETE_SOURCE_REVIEW", "审核记录 ID 重复")
			return
		}
		approved[id] = true
	}
	complete := len(bindings) > 0 && len(approved) == len(bindings)
	for _, binding := range bindings {
		complete = complete && approved[binding.Record.ID]
	}
	if !complete {
		apiError(w, 422, "INCOMPLETE_SOURCE_REVIEW", "只有逐条核对全部原始记录后，才能提交这份完整来源审核")
		return
	}
	sort.Strings(input.ApprovedRecordIDs)
	input.ReviewedAt = nowText()
	reviewed, err := loadReference(bank, auditOptions(bank, entry, input))
	if err != nil {
		apiError(w, 422, "INVALID_EXPERIMENT_POLICY", "实验目标范围或重新计算未通过检查")
		return
	}
	next := s.copyAudits()
	next.Reviews[bank.ID] = input
	// A changed policy/review requires explicit activation again.
	if next.ActiveBankID == bank.ID {
		next.ActiveBankID, next.ActiveSHA256 = "", ""
	}
	if err = s.writeReferenceAudits(next); err != nil {
		apiError(w, 503, "LOCAL_REVIEW_NOT_SAVED", "本机审核记录保存失败，本次审核尚未生效")
		return
	}
	s.references[bank.ID] = reviewed
	apiJSON(w, 200, map[string]any{"reviewed": true, "validation": reviewed.report})
}

func (s *Service) activateReference(w http.ResponseWriter, r *http.Request) {
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
	id := r.PathValue("id")
	bank, err := s.store.Bank(id)
	entry := s.references[id]
	if err != nil || entry == nil || !entry.compatible || !entry.report.IdentityValidated {
		apiError(w, 409, "REFERENCE_NOT_VALIDATED", "参考库需要真实来源审核、独立验证通过，并与当前实际测试协议一致")
		return
	}
	next := s.copyAudits()
	next.ActiveBankID, next.ActiveSHA256 = id, bank.SHA256
	if s.writeReferenceAudits(next) != nil {
		apiError(w, 503, "LOCAL_REVIEW_NOT_SAVED", "本机参考库选择保存失败")
		return
	}
	apiJSON(w, 200, map[string]any{"active_bank_id": id, "bank_version": entry.report.BankVersion, "corpus_sha256": entry.report.CorpusSHA256})
}

func (s *Service) clearReferenceAudits() error {
	return s.writeReferenceAudits(referenceAudits{Schema: 1, Reviews: map[string]sourceReview{}})
}

func (s *Service) forgetReference(id string) error {
	next := s.copyAudits()
	delete(next.Reviews, id)
	if next.ActiveBankID == id {
		next.ActiveBankID, next.ActiveSHA256 = "", ""
	}
	return s.writeReferenceAudits(next)
}

func (s *Service) activeReference() *referenceEntry {
	bank, err := s.store.Bank(s.audits.ActiveBankID)
	entry := s.references[s.audits.ActiveBankID]
	if err != nil || bank.SHA256 != s.audits.ActiveSHA256 || entry == nil || !entry.compatible || !entry.report.IdentityValidated {
		return nil
	}
	return entry
}

// Restore input and export share the nested-string privacy boundary too.
func (s *Service) transferHasCredentials(raw []byte) bool {
	findings, err := attribution.CredentialFindingsJSON(raw)
	return err != nil || len(findings) > 0 || s.containsPrivateValue(raw)
}
