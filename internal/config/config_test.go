package config

import (
	"strings"
	"testing"
	"time"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("S3_BUCKET", "test-bucket")
	t.Setenv("LOCAL_PATH", "/data")
	t.Setenv("CRON_EXPRESSION", "0 0 * * *")
	t.Setenv("DELETION_BEHAVIOR", "prompt")
	t.Setenv("STALE_TIME", "180d")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_FROM", "from@example.com")
	t.Setenv("SMTP_TO", "to@example.com")
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{name: "days format", input: "180d", want: 180 * 24 * time.Hour},
		{name: "hours format", input: "4320h", want: 4320 * time.Hour},
		{name: "minutes format", input: "15m", want: 15 * time.Minute},
		{name: "empty", input: "", wantErr: true},
		{name: "negative days", input: "-1d", wantErr: true},
		{name: "negative duration", input: "-1h", wantErr: true},
		{name: "invalid format", input: "abc", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDuration(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for input %q: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("parseDuration(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestLoadSuccess(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("S3_PREFIX", "backups")
	t.Setenv("LISTEN_PORT", "9090")
	t.Setenv("APPROVAL_TOKEN_LIFETIME", "24h")
	t.Setenv("HOSTNAME", "cleaner.example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.DeletionBehavior != DeletionPrompt {
		t.Fatalf("DeletionBehavior = %q, want %q", cfg.DeletionBehavior, DeletionPrompt)
	}
	if cfg.StaleTime != 180*24*time.Hour {
		t.Fatalf("StaleTime = %v, want %v", cfg.StaleTime, 180*24*time.Hour)
	}
	if cfg.ListenPort != 9090 {
		t.Fatalf("ListenPort = %d, want 9090", cfg.ListenPort)
	}
	if cfg.ApprovalTokenLifetime != 24*time.Hour {
		t.Fatalf("ApprovalTokenLifetime = %v, want 24h", cfg.ApprovalTokenLifetime)
	}
}

func TestLoadInvalidDeletionBehavior(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("DELETION_BEHAVIOR", "invalid")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid deletion behavior")
	}
	if !strings.Contains(err.Error(), "DELETION_BEHAVIOR") {
		t.Fatalf("expected DELETION_BEHAVIOR error, got: %v", err)
	}
}

func TestApprovalBaseURL(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "localhost fallback",
			cfg:  Config{ListenPort: 8080},
			want: "http://localhost:8080",
		},
		{
			name: "hostname auto https",
			cfg:  Config{Hostname: "cleaner.example.com"},
			want: "https://cleaner.example.com",
		},
		{
			name: "preserve provided scheme and trim slash",
			cfg:  Config{Hostname: "https://cleaner.example.com/"},
			want: "https://cleaner.example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.ApprovalBaseURL()
			if got != tc.want {
				t.Fatalf("ApprovalBaseURL() = %q, want %q", got, tc.want)
			}
		})
	}
}
