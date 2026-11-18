package attribution

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// CredentialFinding never includes a credential value. A clean scan is only a
// common-pattern check, not a guarantee about arbitrary opaque import strings.
type CredentialFinding struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

var credentialText = regexp.MustCompile(`(?i)(?:bearer\s+\S+|sk-(?:proj-|svcacct-)?[a-z0-9_-]{16,}|-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----|(?:api[_-]?key|authorization|password|client[_-]?secret|gateway[_-]?key|route[_-]?key)\s*[:=]\s*\S+)`)

func credentialKey(key string) bool {
	key = strings.ToLower(key)
	key = strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r == ' ' {
			return -1
		}
		return r
	}, key)
	switch key {
	case "authorization", "proxyauthorization", "apikey", "accesskey", "accesstoken", "refreshtoken", "token", "password", "passwd", "secret", "clientsecret", "secretkey", "privatekey", "gatewaykey", "routekey", "webkey", "credentials":
		return true
	}
	return false
}

// CredentialFindingsJSON checks ALL fields, raw JSON strings inside a bundle,
// provenance evidence and textual credential patterns. It rejects ambiguous
// or malformed nested JSON and leaves approval/rejection policy to the caller.
func CredentialFindingsJSON(raw []byte) ([]CredentialFinding, error) {
	var data any
	if err := strictJSON(raw, &data); err != nil {
		return nil, fmt.Errorf("credential scan invalid or ambiguous JSON")
	}
	findings := []CredentialFinding{}
	var walk func(any, string, int) error
	walk = func(value any, path string, depth int) error {
		if depth > maxJSONDepth {
			return fmt.Errorf("credential scan nesting limit exceeded")
		}
		switch v := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for key := range v {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if credentialText.MatchString(key) {
					findings = append(findings, CredentialFinding{Path: path + ".[redacted-key]", Reason: "credential_text_in_key"})
					continue
				}
				childPath := path + "." + key
				if credentialKey(key) {
					findings = append(findings, CredentialFinding{Path: childPath, Reason: "credential_field"})
				}
				if err := walk(v[key], childPath, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for i, item := range v {
				if err := walk(item, fmt.Sprintf("%s[%d]", path, i), depth+1); err != nil {
					return err
				}
			}
		case string:
			if credentialText.MatchString(v) {
				findings = append(findings, CredentialFinding{Path: path, Reason: "credential_text"})
			}
			trimmed := strings.TrimSpace(v)
			if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
				var nested any
				if err := strictJSON([]byte(v), &nested); err != nil {
					return fmt.Errorf("credential scan malformed nested JSON at %s", path)
				}
				return walk(nested, path+"(json)", depth+1)
			}
		}
		return nil
	}
	if err := walk(data, "$", 0); err != nil {
		return nil, err
	}
	return findings, nil
}
