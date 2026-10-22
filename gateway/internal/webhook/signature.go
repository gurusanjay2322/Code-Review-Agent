package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

var (
	ErrMissingSignature = errors.New("missing X-Hub-Signature-256 header")
	ErrInvalidSignature = errors.New("invalid webhook signature")
)

// VerifySignature checks a GitHub "sha256=<hex>" signature over the exact raw request body.
func VerifySignature(secret []byte, body []byte, header string) error {
	if header == "" {
		return ErrMissingSignature
	}
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return ErrInvalidSignature
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return ErrInvalidSignature
	}
	return nil
}

func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
