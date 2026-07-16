package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidKey = errors.New("invalid API key")

// Principal contains the authenticated client and the effective quota for the
// current route. Raw API keys never leave the authentication boundary.
type Principal struct {
	KeyID         string
	ClientID      string
	ClientName    string
	Plan          string
	RatePerSecond int64
	BurstCapacity int64
}

type Store interface {
	LookupActiveKey(context.Context, []byte, string) (Principal, error)
}

type Authenticator interface {
	Authenticate(context.Context, string, string) (Principal, error)
}

type Service struct {
	store  Store
	pepper []byte
}

func NewService(store Store, pepper string) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("authentication store is nil")
	}
	if len(pepper) < 16 {
		return nil, fmt.Errorf("API key pepper must contain at least 16 characters")
	}
	return &Service{store: store, pepper: []byte(pepper)}, nil
}

func (s *Service) Authenticate(ctx context.Context, rawKey, route string) (Principal, error) {
	if strings.TrimSpace(rawKey) == "" {
		return Principal{}, ErrInvalidKey
	}
	principal, err := s.store.LookupActiveKey(ctx, Digest(s.pepper, rawKey), route)
	if err != nil {
		return Principal{}, err
	}
	return principal, nil
}

// Digest uses an HMAC pepper so a database leak is not enough to validate
// guessed keys. Keys are high-entropy random values, not user passwords.
func Digest(pepper []byte, rawKey string) []byte {
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte(rawKey))
	return mac.Sum(nil)
}

// GenerateKey returns a raw API key and its safe display prefix. Callers must
// display the raw value once and persist only Digest(pepper, raw).
func GenerateKey() (rawKey, prefix string, err error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", "", fmt.Errorf("generate API key entropy: %w", err)
	}
	rawKey = "gw_live_" + base64.RawURLEncoding.EncodeToString(random)
	return rawKey, Prefix(rawKey), nil
}

func Prefix(rawKey string) string {
	prefixLength := 18
	if len(rawKey) < prefixLength {
		prefixLength = len(rawKey)
	}
	return rawKey[:prefixLength]
}
