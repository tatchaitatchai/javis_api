package config

import (
	"strings"
	"testing"
	"time"
)

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://localhost/javis",
		"APP_ENV":      "development",
		"OTP_SENDER":   "debug",
		"OTP_PEPPER":   strings.Repeat("p", 32),
	}
}

func TestLoadDefaultsAndOverrides(t *testing.T) {
	env := validEnv()
	getenv := func(key string) string { return env[key] }
	cfg, err := Load(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8080" || cfg.DBHealthTimeout != 2*time.Second {
		t.Fatal("unexpected defaults")
	}
	env["HTTP_ADDR"] = "[::1]:9090"
	env["DB_HEALTH_TIMEOUT"] = "500ms"
	cfg, err = Load(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "[::1]:9090" || cfg.DBHealthTimeout != 500*time.Millisecond {
		t.Fatal("overrides were not applied")
	}
}

func TestLoadRejectsInvalidConfigurationWithoutLeakingValues(t *testing.T) {
	tests := []struct {
		name, key, value string
	}{
		{"missing database", "DATABASE_URL", ""},
		{"blank database", "DATABASE_URL", " "},
		{"bad scheme", "DATABASE_URL", "https://secret-user:super-secret@localhost/javis"},
		{"missing host", "DATABASE_URL", "postgres:///javis"},
		{"missing database name", "DATABASE_URL", "postgres://localhost"},
		{"malformed URL", "DATABASE_URL", "postgres://secret-user:super-secret@%/javis"},
		{"bad address", "HTTP_ADDR", "localhost"},
		{"bad port", "HTTP_ADDR", "localhost:abc"},
		{"zero port", "HTTP_ADDR", "localhost:0"},
		{"large port", "HTTP_ADDR", "localhost:65536"},
		{"invalid timeout", "DB_HEALTH_TIMEOUT", "later"},
		{"zero timeout", "DB_HEALTH_TIMEOUT", "0s"},
		{"negative timeout", "DB_HEALTH_TIMEOUT", "-1s"},
		{"excessive timeout", "DB_HEALTH_TIMEOUT", "6s"},
		{"unknown environment", "APP_ENV", "staging"},
		{"missing OTP sender", "OTP_SENDER", ""},
		{"unknown OTP sender", "OTP_SENDER", "super-secret"},
		{"missing pepper", "OTP_PEPPER", ""},
		{"short pepper", "OTP_PEPPER", "super-secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
			env[tt.key] = tt.value
			_, err := Load(func(key string) string { return env[key] })
			if err == nil {
				t.Fatal("expected configuration error")
			}
			if strings.Contains(err.Error(), "secret-user") || strings.Contains(err.Error(), "super-secret") {
				t.Fatal("configuration error exposed a secret")
			}
		})
	}
}

func TestOTPDebugModeOnlyRunsInDevelopment(t *testing.T) {
	for _, appEnv := range []string{"", "production"} {
		env := validEnv()
		env["APP_ENV"] = appEnv
		if _, err := Load(func(key string) string { return env[key] }); err == nil {
			t.Fatalf("APP_ENV %q must refuse OTP_SENDER=debug", appEnv)
		}
	}
}
