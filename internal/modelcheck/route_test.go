package modelcheck

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func signedRoute(key, id, target, provider, content, finish string) RouteEvidence {
	digest := sha256.Sum256([]byte(content + "\n" + finish))
	e := RouteEvidence{Version: 1, ProbeID: id, TargetModelID: target, ProviderID: provider, ProviderModel: "solo", AttemptCount: 2, Source: "local-gateway-middleware", ResponseSHA256: hex.EncodeToString(digest[:])}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(e.payload()))
	e.MAC = hex.EncodeToString(mac.Sum(nil))
	return e
}

func TestProtocolProofRequiresV2ExactPacketAndFinalObserver(t *testing.T) {
	packet := map[string]any{"model": "channel/solo", "messages": []map[string]string{{"role": "user", "content": "检测 < > & 🦜"}}, "stream": false, "max_tokens": 2048, "_magpie_model_check_probe": map[string]any{"version": 1, "id": strings.Repeat("1", 32)}}
	canonical := `{"max_tokens":2048,"messages":[{"content":"检测 < > & 🦜","role":"user"}],"stream":false}`
	sha := sha256.Sum256([]byte(canonical))
	expected := hex.EncodeToString(sha[:])
	if got := requestBodySHA256(packet); got != expected {
		t.Fatal("Go/ECMAScript request canonicalization differs", got, expected)
	}
	e := RouteEvidence{Version: 2, RequestSHA256: expected}
	if !verifiedProtocol(e, true, true, packet) {
		t.Fatal("exact authenticated final packet rejected")
	}
	for _, condition := range []string{"v1", "forged", "observer", "packet"} {
		changed, auth, guard, body := e, true, true, packet
		switch condition {
		case "v1":
			changed.Version = 1
		case "forged":
			auth = false
		case "observer":
			guard = false
		case "packet":
			body = map[string]any{"messages": []string{"changed"}}
		}
		if verifiedProtocol(changed, auth, guard, body) {
			t.Fatal("accepted incomplete protocol proof", condition)
		}
	}
}

func TestRouteRequiresPrivateSignatureAndExpectedProvider(t *testing.T) {
	key, id, target := strings.Repeat("a", 64), strings.Repeat("1", 32), "channel/solo"
	e := signedRoute(key, id, target, "channel", "17,29,41", "stop")
	if !verifyRoute(e, key, id, target, "channel", "17,29,41", "stop") {
		t.Fatal("genuine middleware evidence rejected")
	}
	for _, field := range []string{"mac", "nonce", "target", "provider", "content", "finish"} {
		changed, nonce, model, expected, text, finish := e, id, target, "channel", "17,29,41", "stop"
		switch field {
		case "mac":
			changed.MAC = strings.Repeat("0", 64)
		case "nonce":
			nonce = strings.Repeat("2", 32)
		case "target":
			model = "other/solo"
		case "provider":
			expected = "other"
		case "content":
			text = "1,2,3"
		case "finish":
			finish = "length"
		}
		if verifyRoute(changed, key, nonce, model, expected, text, finish) {
			t.Fatalf("accepted tampered %s", field)
		}
	}
	if verifyRoute(e, key, id, target, "", "17,29,41", "stop") {
		t.Fatal("unspecified provider became verified")
	}
	if verifyRoute(e, "", id, target, "channel", "17,29,41", "stop") {
		t.Fatal("missing private key became verified")
	}
}

func TestRouteReportNeverExportsPrivateKeyOrMAC(t *testing.T) {
	e := signedRoute(strings.Repeat("a", 64), strings.Repeat("1", 32), "channel/solo", "channel", "17", "stop")
	public := e.Public(true)
	if _, ok := public["mac"]; ok {
		t.Fatal("internal signature exported")
	}
	if _, ok := public["local_proof"]; ok {
		t.Fatal("private proof exported")
	}
	if public["authenticated"] != true || public["provider_id"] != "channel" {
		t.Fatal(public)
	}
}

func TestV3ResponseEnvelopeRejectsConsumedFieldTampering(t *testing.T) {
	raw:=[]byte(`{"model":"self-label","usage":{"prompt_tokens":7,"completion_tokens":9},"choices":[{"message":{"content":"17"},"finish_reason":"stop"}]}`)
	var body map[string]any
	json.Unmarshal(raw,&body)
	var encoded bytes.Buffer
	encoder:=json.NewEncoder(&encoded);encoder.SetEscapeHTML(false);encoder.Encode(body)
	e:=RouteEvidence{Version:3,EnvelopeSHA256:digestBytes(bytes.TrimSuffix(encoded.Bytes(),[]byte("\n")))}
	if !verifyResponseEnvelope(e,raw) { t.Fatal("unaltered full response rejected") }
	for _,replacement:=range []string{strings.Replace(string(raw),`"prompt_tokens":7`,`"prompt_tokens":999999`,1),strings.Replace(string(raw),"self-label","tampered",1)} {
		if verifyResponseEnvelope(e,[]byte(replacement)) { t.Fatal("changed consumed response accepted") }
	}
	e.StreamSHA256="terminal-proof-is-not-a-whole-JSON-response"
	if verifyResponseEnvelope(e,raw) { t.Fatal("stream proof accepted as a full nonstream envelope") }
}
