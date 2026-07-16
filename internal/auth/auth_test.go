package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeStore struct {
	wantDigest []byte
	principal  Principal
}

func (f fakeStore) LookupActiveKey(_ context.Context, digest []byte, _ string) (Principal, error) {
	if !hmacEqual(digest, f.wantDigest) {
		return Principal{}, ErrInvalidKey
	}
	return f.principal, nil
}

func TestAuthenticateUsesPepperedDigest(t *testing.T) {
	pepper := "test-pepper-at-least-sixteen"
	want := Principal{KeyID: "key-1", ClientID: "client-1", RatePerSecond: 5, BurstCapacity: 10}
	service, err := NewService(fakeStore{wantDigest: Digest([]byte(pepper), "secret"), principal: want}, pepper)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	got, err := service.Authenticate(context.Background(), "secret", "/api/users")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if got.KeyID != want.KeyID {
		t.Fatalf("KeyID = %q, want %q", got.KeyID, want.KeyID)
	}
}

func TestAuthenticateRejectsEmptyKey(t *testing.T) {
	service, err := NewService(fakeStore{}, "test-pepper-at-least-sixteen")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.Authenticate(context.Background(), "", "/api/users"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Authenticate() error = %v, want ErrInvalidKey", err)
	}
}

func TestGenerateKey(t *testing.T) {
	first, prefix, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	second, _, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() second error = %v", err)
	}
	if first == second {
		t.Fatal("GenerateKey() produced duplicate values")
	}
	if !strings.HasPrefix(first, "gw_live_") || !strings.HasPrefix(first, prefix) {
		t.Fatalf("unexpected key/prefix: %q %q", first, prefix)
	}
}

func hmacEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for i := range left {
		difference |= left[i] ^ right[i]
	}
	return difference == 0
}
