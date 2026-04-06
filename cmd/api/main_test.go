package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		remote   string
		expected string
	}{
		{
			name:     "X-Forwarded-For single IP",
			headers:  map[string]string{"X-Forwarded-For": "1.2.3.4"},
			remote:   "10.0.0.1:1234",
			expected: "1.2.3.4",
		},
		{
			name:     "X-Forwarded-For multiple IPs",
			headers:  map[string]string{"X-Forwarded-For": "1.2.3.4, 10.0.0.1, 10.0.0.2"},
			remote:   "10.0.0.3:1234",
			expected: "1.2.3.4",
		},
		{
			name:     "X-Real-Ip",
			headers:  map[string]string{"X-Real-Ip": "5.6.7.8"},
			remote:   "10.0.0.1:1234",
			expected: "5.6.7.8",
		},
		{
			name:     "X-Forwarded-For takes precedence over X-Real-Ip",
			headers:  map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Real-Ip": "5.6.7.8"},
			remote:   "10.0.0.1:1234",
			expected: "1.2.3.4",
		},
		{
			name:     "fallback to RemoteAddr",
			headers:  map[string]string{},
			remote:   "10.0.0.1:1234",
			expected: "10.0.0.1:1234",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{
				RemoteAddr: tt.remote,
				Header:     http.Header{},
			}
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			assert.Equal(t, tt.expected, clientIP(r))
		})
	}
}
