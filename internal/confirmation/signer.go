package confirmation

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidToken = errors.New("invalid confirmation token")
	ErrExpiredToken = errors.New("expired confirmation token")
	ErrPlanChanged  = errors.New("confirmed plan has changed")
)

type Signer struct {
	key []byte
	now func() time.Time
}
type claims struct {
	Digest    string `json:"digest"`
	ExpiresAt int64  `json:"expiresAt"`
}

func New(key []byte) (*Signer, error) {
	if len(key) < 32 {
		return nil, errors.New("confirmation key must contain at least 32 bytes")
	}
	copyKey := append([]byte(nil), key...)
	return &Signer{key: copyKey, now: time.Now}, nil
}
func (s *Signer) WithClock(now func() time.Time) *Signer { s.now = now; return s }

func (s *Signer) Issue(plan any, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > 15*time.Minute {
		return "", errors.New("confirmation TTL must be between 1ns and 15m")
	}
	digest, err := Digest(plan)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims{Digest: digest, ExpiresAt: s.now().Add(ttl).Unix()})
	if err != nil {
		return "", err
	}
	signature := s.sign(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
func (s *Signer) Verify(token string, plan any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrInvalidToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, s.sign(payload)) {
		return ErrInvalidToken
	}
	var value claims
	if err := json.Unmarshal(payload, &value); err != nil {
		return ErrInvalidToken
	}
	if s.now().Unix() > value.ExpiresAt {
		return ErrExpiredToken
	}
	digest, err := Digest(plan)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(value.Digest), []byte(digest)) {
		return ErrPlanChanged
	}
	return nil
}
func (s *Signer) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}
func Digest(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
