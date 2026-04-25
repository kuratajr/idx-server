package utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func b64urlEncode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func b64urlDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// MakeSignedToken returns a token of form "<payload_b64url>.<sig_b64url>".
// Signature is HMAC-SHA256(secret, payload_b64url).
func MakeSignedToken(secret string, payload any) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	payloadB64 := b64urlEncode(b)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payloadB64))
	sig := b64urlEncode(mac.Sum(nil))
	return payloadB64 + "." + sig, nil
}

// VerifySignedToken validates token signature and unmarshals JSON payload into out.
func VerifySignedToken(secret, token string, out any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return errors.New("invalid token")
	}
	payloadB64 := parts[0]
	sigB64 := parts[1]

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payloadB64))
	want := b64urlEncode(mac.Sum(nil))
	if !hmac.Equal([]byte(sigB64), []byte(want)) {
		return errors.New("invalid token signature")
	}

	b, err := b64urlDecode(payloadB64)
	if err != nil {
		return errors.New("invalid token payload")
	}
	if err := json.Unmarshal(b, out); err != nil {
		return errors.New("invalid token payload")
	}
	return nil
}

// BuildIDXScriptURL builds the URL nodes will curl.
func BuildIDXScriptURL(installHost string, agentTLS bool, token string) string {
	scheme := "http"
	if agentTLS {
		scheme = "https"
	}
	host := strings.TrimSpace(installHost)
	return fmt.Sprintf("%s://%s/api/v1/idx/script?token=%s", scheme, host, token)
}

