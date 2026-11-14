package auth

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestAdminBcryptHashIsValidatedAtStartup(t *testing.T) {
	_, err := NewUserAuthenticator("admin", "", "not-a-bcrypt-hash")
	if err == nil || !strings.Contains(err.Error(), "BUGBARN_ADMIN_PASSWORD_BCRYPT") {
		t.Fatalf("err = %v, want a clear error naming BUGBARN_ADMIN_PASSWORD_BCRYPT", err)
	}
}

func TestAdminBcryptHashLogsIn(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("hunter2hunter2"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	// Secrets arrive with a trailing newline from files and some tooling.
	a, err := NewUserAuthenticator("admin", "", string(hash)+"\n")
	if err != nil {
		t.Fatalf("a valid hash was rejected: %v", err)
	}
	if !a.Valid("admin", "hunter2hunter2") || a.Valid("admin", "wrong") {
		t.Fatal("the pre-hashed admin password does not authenticate correctly")
	}
}
