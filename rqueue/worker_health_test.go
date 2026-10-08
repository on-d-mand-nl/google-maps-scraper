package rqueue

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPWorkerHealth(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		reachable  bool
	}{
		{"healthy", `{"status":"ok","jobs_processed":2,"results_collected":8}`, 200, true},
		{"invalid", `invalid`, 200, false},
		{"error", `{"status":"ok"}`, 503, false},
		{"unhealthy", `{"status":"error"}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code); _, _ = w.Write([]byte(tc.body)) }))
			defer server.Close()
			result := checkHTTPWorkerHealth(context.Background(), strings.TrimPrefix(server.URL, "http://"))
			if result.Reachable != tc.reachable || result.CheckedAt == "" {
				t.Fatalf("unexpected health: %+v", result)
			}
			if tc.reachable && result.ResultsCollected != 8 {
				t.Fatalf("metrics lost: %+v", result)
			}
		})
	}
}
