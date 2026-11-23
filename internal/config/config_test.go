package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantPort  string
		wantProxy string
		wantTTL   time.Duration
		wantConc  int
		wantErr   bool
	}{
		{
			name:      "default values",
			env:       map[string]string{},
			wantPort:  "8080",
			wantProxy: "",
			wantTTL:   15 * time.Minute,
			wantConc:  4,
		},
		{
			name: "custom env values",
			env: map[string]string{
				"PORT":                     "9090",
				"PROXY_URL":                "http://user:pass@proxy.local:3128",
				"FILE_TTL":                 "30m",
				"MAX_CONCURRENT_DOWNLOADS": "8",
				"BASE_URL":                 "http://ytdlp.internal:9090/",
			},
			wantPort:  "9090",
			wantProxy: "http://user:pass@proxy.local:3128",
			wantTTL:   30 * time.Minute,
			wantConc:  8,
		},
		{
			name: "invalid duration",
			env: map[string]string{
				"FILE_TTL": "not-a-duration",
			},
			wantErr: true,
		},
		{
			name: "non-positive concurrency",
			env: map[string]string{
				"MAX_CONCURRENT_DOWNLOADS": "0",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"PORT", "PROXY_URL", "FILE_TTL", "MAX_CONCURRENT_DOWNLOADS", "BASE_URL"} {
				t.Setenv(key, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := Load()
			is := assert.New(t)
			req := require.New(t)

			if tt.wantErr {
				req.Error(err)
				return
			}

			req.NoError(err)
			is.Equal(tt.wantPort, cfg.Port)
			is.Equal(tt.wantProxy, cfg.ProxyURL)
			is.Equal(tt.wantTTL, cfg.FileTTL)
			is.Equal(tt.wantConc, cfg.MaxConcurrentDownloads)
			if tt.env["BASE_URL"] != "" {
				is.Equal("http://ytdlp.internal:9090", cfg.BaseURL)
			}
		})
	}
}
