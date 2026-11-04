package config

import "testing"

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("HTTP_ADDR", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8080" {
		t.Errorf("default addr: got %q", c.HTTPAddr)
	}
}

func TestLoadRequiresSecretAndDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "short")
	if _, err := Load(); err == nil {
		t.Error("short JWT secret must be rejected")
	}
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Error("missing DATABASE_URL must be rejected")
	}
}
