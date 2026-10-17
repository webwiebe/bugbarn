package main

import (
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/wiebe-xyz/bugbarn/internal/config"
)

func TestNewUserAuthenticatorAddsTheSmokeLoginOnlyWhenConfigured(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("smoke-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AdminUsername: "admin", AdminPassword: "admin-pass"}

	users, err := newUserAuthenticator(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if users.Valid("smoke", "smoke-pass") {
		t.Fatal("the smoke login must be disabled by default")
	}

	cfg.SmokeUsername = "smoke"
	cfg.SmokePasswordHash = string(hash)
	users, err = newUserAuthenticator(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !users.Valid("smoke", "smoke-pass") || !users.Valid("admin", "admin-pass") {
		t.Fatal("both logins must work once the smoke login is configured")
	}

	cfg.SmokePasswordHash = ""
	if _, err := newUserAuthenticator(cfg); err == nil {
		t.Fatal("a smoke username without a hash must stop startup")
	}
}
