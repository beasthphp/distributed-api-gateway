package usage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Event is the bounded, privacy-conscious record emitted after an
// authenticated gateway request. It deliberately excludes API keys, headers,
// query strings, and request or response bodies.
type Event struct {
	ID             string    `json:"event_id"`
	RequestID      string    `json:"request_id"`
	APIKeyID       string    `json:"api_key_id"`
	ClientID       string    `json:"client_id"`
	Route          string    `json:"route"`
	Method         string    `json:"method"`
	StatusCode     int       `json:"status_code"`
	DurationMicros int64     `json:"duration_microseconds"`
	ResponseBytes  int64     `json:"response_bytes"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func (e Event) Validate() error {
	switch {
	case strings.TrimSpace(e.ID) == "":
		return fmt.Errorf("event ID is required")
	case strings.TrimSpace(e.APIKeyID) == "":
		return fmt.Errorf("API key ID is required")
	case strings.TrimSpace(e.ClientID) == "":
		return fmt.Errorf("client ID is required")
	case !strings.HasPrefix(e.Route, "/"):
		return fmt.Errorf("normalized route is required")
	case strings.TrimSpace(e.Method) == "":
		return fmt.Errorf("HTTP method is required")
	case e.StatusCode < 100 || e.StatusCode > 599:
		return fmt.Errorf("status code must be between 100 and 599")
	case e.DurationMicros < 0:
		return fmt.Errorf("duration cannot be negative")
	case e.ResponseBytes < 0:
		return fmt.Errorf("response bytes cannot be negative")
	case e.OccurredAt.IsZero():
		return fmt.Errorf("occurrence time is required")
	default:
		return nil
	}
}

// NewEventID returns an RFC 4122 version 4 UUID without adding another
// runtime dependency.
func NewEventID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate usage event ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
