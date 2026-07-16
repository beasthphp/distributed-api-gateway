package usage

import (
	"regexp"
	"testing"
)

func TestNewEventIDReturnsVersion4UUID(t *testing.T) {
	id, err := NewEventID()
	if err != nil {
		t.Fatalf("NewEventID() error = %v", err)
	}
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !pattern.MatchString(id) {
		t.Fatalf("NewEventID() = %q, want RFC 4122 version 4 UUID", id)
	}
}

func TestEventValidationRejectsMissingIdentity(t *testing.T) {
	event := testEvent()
	event.ClientID = ""
	if err := event.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want missing client error")
	}
}
