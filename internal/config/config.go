package config

import (
	"errors"
	"os"
)

type Config struct {
	DatabaseURL string
	JWTSecret   string
	HTTPAddr    string
}

// Load reads configuration from the environment. The JWT secret is required
// and must never be committed to the repository.
func Load() (Config, error) {
	c := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		JWTSecret:   os.Getenv("JWT_SECRET"),
		HTTPAddr:    os.Getenv("HTTP_ADDR"),
	}
	if c.HTTPAddr == "" {
		c.HTTPAddr = ":8080"
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if len(c.JWTSecret) < 16 {
		return c, errors.New("JWT_SECRET is required (at least 16 characters)")
	}
	return c, nil
}
