package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("HTTP_ADDR", "")
	c, err := Load()
	if err != nil || c.Addr != ":8080" {
		t.Fatalf("config=%+v err=%v", c, err)
	}
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	if c, err = Load(); err != nil || c.Addr != "127.0.0.1:9090" {
		t.Fatalf("config=%+v err=%v", c, err)
	}
	for _, secret := range []string{"", "too-short", "replace-this-with-a-random-secret-before-starting"} {
		t.Setenv("JWT_SECRET", secret)
		if _, err := Load(); err == nil {
			t.Fatal("accepted an unsafe secret")
		}
	}
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("accepted missing database URL")
	}
}
