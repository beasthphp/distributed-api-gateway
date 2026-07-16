package mockservice

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// NewHandler creates a deterministic mock backend used to demonstrate gateway
// routing without hiding business logic inside the gateway itself.
func NewHandler(service string) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"service": service, "status": "ok"})
	})

	switch service {
	case "user-service":
		mux.HandleFunc("GET /api/users", listUsers)
		mux.HandleFunc("GET /api/users/{id}", getUser)
	case "order-service":
		mux.HandleFunc("GET /api/orders", listOrders)
		mux.HandleFunc("GET /api/orders/{id}", getOrder)
	default:
		return nil, fmt.Errorf("unknown mock service %q", service)
	}

	return requestLog(service, mux), nil
}

func listUsers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"users": []map[string]string{
			{"id": "1", "name": "Ada"},
			{"id": "2", "name": "Linus"},
		},
	})
}

func getUser(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "name": "Demo User " + id})
}

func listOrders(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"orders": []map[string]any{
			{"id": "101", "status": "created", "amount": 499},
			{"id": "102", "status": "shipped", "amount": 1299},
		},
	})
}

func getOrder(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "created", "amount": 799})
}

func requestLog(service string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		fmt.Printf("service=%s method=%s path=%s request_id=%s duration_ms=%d\n",
			service,
			r.Method,
			r.URL.Path,
			r.Header.Get("X-Gateway-Request-ID"),
			time.Since(started).Milliseconds(),
		)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
