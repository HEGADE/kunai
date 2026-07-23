package codex

import (
	"encoding/base64"
	"strings"
	"testing"
)

// validGPTSig builds a transport-valid GPT/Codex reasoning signature: a Fernet-shape
// token (version 0x80, 73 bytes: 1+8 header, 16 IV, 16 ciphertext, 32 HMAC) whose
// base64url starts "gAAAA".
func validGPTSig() string {
	payload := make([]byte, 73)
	payload[0] = 0x80 // version; bytes 1..4 stay zero so the base64 begins gAAAA
	for i := 9; i < len(payload); i++ {
		payload[i] = byte(i*7 + 3)
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

// A reasoning signature from one provider must not be replayed to another: a Codex
// (GPT) reasoning block left in history after switching a session to Grok must be
// dropped, or xAI rejects the whole request ("could not decrypt the provided
// encrypted_content"). Kept for a Codex target, dropped for a Grok target.
func TestCrossProviderReasoningSignatureDropped(t *testing.T) {
	gptSig := validGPTSig()
	if !IsValidGPTReasoningSignature(gptSig) {
		t.Fatalf("test setup: %q is not a valid GPT signature", gptSig)
	}
	inbound := []byte(`{"model":"x","messages":[` +
		`{"role":"user","content":"hi"},` +
		`{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"` + gptSig + `"},{"type":"text","text":"hello"}]},` +
		`{"role":"user","content":"again"}` +
		`]}`)

	codexOut := ConvertClaudeRequestToCodex("gpt-5.4", inbound, false)
	if !strings.Contains(string(codexOut), gptSig) {
		t.Error("Codex target should keep the GPT reasoning signature")
	}
	grokOut := ConvertClaudeRequestToCodex("grok-4.5", inbound, false)
	if strings.Contains(string(grokOut), gptSig) {
		t.Error("Grok target must NOT replay the Codex/GPT reasoning signature")
	}
}
