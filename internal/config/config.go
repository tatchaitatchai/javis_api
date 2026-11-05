package config

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// EnvDevelopment is the only environment that allows OTP debug mode. An
	// unset APP_ENV is treated as production so debug mode can never slip through.
	EnvDevelopment = "development"
	EnvProduction  = "production"

	// OTPSenderDebug sends no SMS and returns the code in the API response.
	OTPSenderDebug = "debug"

	minOTPPepperLength = 32
)

// Config contains process configuration. DatabaseURL and OTPPepper must never be logged.
type Config struct {
	AppEnv          string
	HTTPAddr        string
	DatabaseURL     string
	DBHealthTimeout time.Duration
	OTPSender       string
	OTPPepper       string
}

// Load validates environment values without including sensitive values in errors.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		AppEnv:          EnvProduction,
		HTTPAddr:        "127.0.0.1:8080",
		DatabaseURL:     getenv("DATABASE_URL"),
		DBHealthTimeout: 2 * time.Second,
		OTPSender:       getenv("OTP_SENDER"),
		OTPPepper:       getenv("OTP_PEPPER"),
	}
	if value := getenv("APP_ENV"); value != "" {
		if value != EnvDevelopment && value != EnvProduction {
			return Config{}, errors.New("APP_ENV must be development or production")
		}
		cfg.AppEnv = value
	}
	if value := getenv("HTTP_ADDR"); value != "" {
		cfg.HTTPAddr = value
	}
	_, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return Config{}, errors.New("HTTP_ADDR must be a host:port address")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return Config{}, errors.New("HTTP_ADDR must use a port between 1 and 65535")
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	databaseURL, err := url.Parse(cfg.DatabaseURL)
	if err != nil || (databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql") || databaseURL.Hostname() == "" || strings.Trim(databaseURL.Path, "/") == "" {
		return Config{}, errors.New("DATABASE_URL must be a PostgreSQL URL with a host and database name")
	}
	if value := getenv("DB_HEALTH_TIMEOUT"); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil || timeout <= 0 || timeout > 5*time.Second {
			return Config{}, errors.New("DB_HEALTH_TIMEOUT must be a positive duration no greater than 5s")
		}
		cfg.DBHealthTimeout = timeout
	}
	if cfg.OTPSender != OTPSenderDebug {
		return Config{}, errors.New("OTP_SENDER must be debug: no SMS provider is connected yet")
	}
	if cfg.OTPSender == OTPSenderDebug && cfg.AppEnv != EnvDevelopment {
		return Config{}, errors.New("OTP_SENDER=debug requires APP_ENV=development")
	}
	if len(cfg.OTPPepper) < minOTPPepperLength {
		return Config{}, errors.New("OTP_PEPPER must be at least 32 characters")
	}
	return cfg, nil
}
