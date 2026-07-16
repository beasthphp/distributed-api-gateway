package mockservice

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUserServiceRoute(t *testing.T) {
	h, err := NewHandler("user-service")
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/users/7", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
}

func TestUnknownServiceIsRejected(t *testing.T) {
	if _, err := NewHandler("payments"); err == nil {
		t.Fatal("NewHandler() error = nil, want unknown service error")
	}
}
