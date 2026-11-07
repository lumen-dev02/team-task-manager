package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "a-test-secret-that-is-at-least-32-bytes-long"

func TestTokens(t *testing.T) {
	tokens := New(testSecret)
	raw, err := tokens.Issue(42)
	if err != nil {
		t.Fatal(err)
	}
	id, err := tokens.Verify(raw)
	if err != nil || id != 42 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	if _, err = New(strings.Repeat("x", 32)).Verify(raw); err == nil {
		t.Fatal("accepted wrong key")
	}
	if _, err = tokens.Verify("not.a.token"); err == nil {
		t.Fatal("accepted malformed token")
	}

	now := time.Now()
	valid := jwt.RegisteredClaims{Issuer: issuer, Audience: jwt.ClaimStrings{issuer}, Subject: "42", ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}
	for _, tc := range []struct {
		name   string
		change func(*jwt.RegisteredClaims)
		method jwt.SigningMethod
	}{
		{"expired", func(c *jwt.RegisteredClaims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Minute)) }, jwt.SigningMethodHS256},
		{"missing expiry", func(c *jwt.RegisteredClaims) { c.ExpiresAt = nil }, jwt.SigningMethodHS256},
		{"wrong issuer", func(c *jwt.RegisteredClaims) { c.Issuer = "other" }, jwt.SigningMethodHS256},
		{"wrong audience", func(c *jwt.RegisteredClaims) { c.Audience = jwt.ClaimStrings{"other"} }, jwt.SigningMethodHS256},
		{"invalid subject", func(c *jwt.RegisteredClaims) { c.Subject = "0" }, jwt.SigningMethodHS256},
		{"wrong algorithm", func(c *jwt.RegisteredClaims) {}, jwt.SigningMethodHS384},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := valid
			tc.change(&claims)
			raw, err := jwt.NewWithClaims(tc.method, claims).SignedString([]byte(testSecret))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tokens.Verify(raw); err == nil {
				t.Fatal("accepted invalid token")
			}
		})
	}
}

func TestPasswordHash(t *testing.T) {
	password := "a-long-test-password"
	hash, err := Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	if hash == password || !Match(hash, password) || Match(hash, "incorrect") {
		t.Fatal("invalid password hashing behavior")
	}
	if _, err = Hash(strings.Repeat("a", 73)); err == nil {
		t.Fatal("accepted a password bcrypt cannot hash")
	}
}
