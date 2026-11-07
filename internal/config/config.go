package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Addr        string
	DatabaseURL string
	JWTSecret   string
}

func Load() (Config, error) {
	c := Config{Addr: os.Getenv("HTTP_ADDR"), DatabaseURL: os.Getenv("DATABASE_URL"), JWTSecret: os.Getenv("JWT_SECRET")}
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if len(c.JWTSecret) < 32 || strings.HasPrefix(c.JWTSecret, "replace-") {
		return Config{}, fmt.Errorf("JWT_SECRET must be a generated secret of at least 32 bytes")
	}
	return c, nil
}
