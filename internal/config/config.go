package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// DeletionBehavior controls how stale files are handled.
type DeletionBehavior string

const (
	DeletionPrompt DeletionBehavior = "prompt"
	DeletionAuto   DeletionBehavior = "auto"
)

// Config holds all application configuration parsed from environment variables.
type Config struct {
	// AWS
	AWSRegion string

	// S3
	S3Bucket string
	S3Prefix string

	// Local filesystem
	LocalPath string

	// Scheduling
	CronExpression string

	// Deletion behavior
	DeletionBehavior DeletionBehavior
	StaleTime        time.Duration

	// SMTP
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPTo       string

	// HTTP / Approval
	Hostname              string
	ListenPort            int
	ApprovalTokenLifetime time.Duration
}

// Load reads configuration from environment variables and validates it.
func Load() (*Config, error) {
	cfg := &Config{}

	// AWS - credentials come from standard AWS env vars (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY)
	cfg.AWSRegion = requiredEnv("AWS_REGION")

	// S3
	cfg.S3Bucket = requiredEnv("S3_BUCKET")
	cfg.S3Prefix = os.Getenv("S3_PREFIX")

	// Local
	cfg.LocalPath = requiredEnv("LOCAL_PATH")

	// Schedule
	cfg.CronExpression = requiredEnv("CRON_EXPRESSION")

	// Deletion
	behavior := requiredEnv("DELETION_BEHAVIOR")
	switch strings.ToLower(behavior) {
	case "prompt":
		cfg.DeletionBehavior = DeletionPrompt
	case "auto":
		cfg.DeletionBehavior = DeletionAuto
	default:
		return nil, fmt.Errorf("DELETION_BEHAVIOR must be 'prompt' or 'auto', got %q", behavior)
	}

	staleTimeStr := requiredEnv("STALE_TIME")
	staleTime, err := parseDuration(staleTimeStr)
	if err != nil {
		return nil, fmt.Errorf("invalid STALE_TIME %q: %w", staleTimeStr, err)
	}
	cfg.StaleTime = staleTime

	// SMTP
	cfg.SMTPHost = requiredEnv("SMTP_HOST")
	smtpPort, err := strconv.Atoi(requiredEnv("SMTP_PORT"))
	if err != nil {
		return nil, fmt.Errorf("invalid SMTP_PORT: %w", err)
	}
	cfg.SMTPPort = smtpPort
	cfg.SMTPUsername = os.Getenv("SMTP_USERNAME")
	cfg.SMTPPassword = os.Getenv("SMTP_PASSWORD")
	cfg.SMTPFrom = requiredEnv("SMTP_FROM")
	cfg.SMTPTo = requiredEnv("SMTP_TO")

	// HTTP
	cfg.Hostname = os.Getenv("HOSTNAME")
	cfg.ListenPort = envInt("LISTEN_PORT", 8080)

	tokenLifetimeStr := os.Getenv("APPROVAL_TOKEN_LIFETIME")
	if tokenLifetimeStr != "" {
		tokenLifetime, err := parseDuration(tokenLifetimeStr)
		if err != nil {
			return nil, fmt.Errorf("invalid APPROVAL_TOKEN_LIFETIME %q: %w", tokenLifetimeStr, err)
		}
		cfg.ApprovalTokenLifetime = tokenLifetime
	}
	// 0 means no expiry

	// Validate
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	var missing []string
	if c.AWSRegion == "" {
		missing = append(missing, "AWS_REGION")
	}
	if c.S3Bucket == "" {
		missing = append(missing, "S3_BUCKET")
	}
	if c.LocalPath == "" {
		missing = append(missing, "LOCAL_PATH")
	}
	if c.CronExpression == "" {
		missing = append(missing, "CRON_EXPRESSION")
	}
	if c.SMTPHost == "" {
		missing = append(missing, "SMTP_HOST")
	}
	if c.SMTPFrom == "" {
		missing = append(missing, "SMTP_FROM")
	}
	if c.SMTPTo == "" {
		missing = append(missing, "SMTP_TO")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return nil
}

func requiredEnv(key string) string {
	return os.Getenv(key)
}

func envBool(key string, defaultVal bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return defaultVal
	}
	return b
}

func envInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return i
}

// parseDuration parses a duration string supporting Go's time.Duration format
// plus a "d" suffix for days (e.g., "180d" = 180 * 24h).
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}

	// Check for day suffix
	if strings.HasSuffix(s, "d") {
		daysStr := strings.TrimSuffix(s, "d")
		days, err := strconv.Atoi(daysStr)
		if err != nil {
			return 0, fmt.Errorf("invalid day count %q: %w", daysStr, err)
		}
		if days < 0 {
			return 0, fmt.Errorf("negative duration not allowed: %d days", days)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	// Fall back to Go's standard duration parsing
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("negative duration not allowed: %s", s)
	}
	return d, nil
}

// ApprovalBaseURL returns the base URL for constructing approval links.
func (c *Config) ApprovalBaseURL() string {
	host := c.Hostname
	if host == "" {
		host = fmt.Sprintf("localhost:%d", c.ListenPort)
		return fmt.Sprintf("http://%s", host)
	}
	// If hostname is set, assume it's behind a reverse proxy with HTTPS
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "https://" + host
	}
	return strings.TrimRight(host, "/")
}
