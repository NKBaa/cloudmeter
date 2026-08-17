package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionTerminationRequests(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodPost, "/api/auth/logout", true},
		{http.MethodDelete, "/api/impersonation", true},
		{http.MethodGet, "/api/auth/logout", false},
		{http.MethodPost, "/api/apps", false},
	}
	for _, test := range tests {
		r := httptest.NewRequest(test.method, test.path, nil)
		if got := isSessionTerminationRequest(r); got != test.want {
			t.Errorf("%s %s: got %v, want %v", test.method, test.path, got, test.want)
		}
	}
}
