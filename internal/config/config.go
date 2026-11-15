package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

type Config struct {
	DatabaseURL      string
	JWTSecret        string
	HTTPAddr         string
	LogLevel         slog.Level
	SLASweepInterval time.Duration
}

// Load reads configuration from the environment. The JWT secret is required
// and must never be committed to the repository.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		JWTSecret:        os.Getenv("JWT_SECRET"),
		HTTPAddr:         os.Getenv("HTTP_ADDR"),
		SLASweepInterval: 30 * time.Second,
	}
	if c.HTTPAddr == "" {
		c.HTTPAddr = ":8080"
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if len(c.JWTSecret) < 32 {
		return c, errors.New("JWT_SECRET is required (at least 32 characters)")
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		if err := c.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return c, fmt.Errorf("LOG_LEVEL: %w", err)
		}
	}
	if v := os.Getenv("SLA_SWEEP_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second {
			return c, errors.New("SLA_SWEEP_INTERVAL must be a duration of at least 1s, e.g. 30s")
		}
		c.SLASweepInterval = d
	}
	return c, nil
}
