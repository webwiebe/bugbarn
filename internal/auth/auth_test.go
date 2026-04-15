package auth

import "testing"

func TestValidWhenDisabled(t *testing.T) {
	a := New("")

	if !a.Valid("") {
		t.Fatal("expected disabled auth to accept empty key")
	}

	if !a.Valid("anything") {
		t.Fatal("expected disabled auth to accept any key")
	}
}

func TestValidWhenEnabled(t *testing.T) {
	a := New("secret")

	if a.Valid("") {
		t.Fatal("expected missing key to be rejected")
	}

	if a.Valid("wrong") {
		t.Fatal("expected wrong key to be rejected")
	}

	if !a.Valid("secret") {
		t.Fatal("expected exact key to be accepted")
	}
}
