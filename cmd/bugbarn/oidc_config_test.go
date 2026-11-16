package main

import (
	"log/slog"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/config"
)

func TestBuildOIDCClientPassesTheOrganization(t *testing.T) {
	client := buildOIDCClient(config.Config{
		OIDCIssuer:         "https://iam.example.com",
		OIDCClientID:       "bugbarn",
		OIDCClientSecret:   "sek",
		OIDCRedirectURL:    "https://bugbarn.example.com/api/v1/oidc/callback",
		OIDCOrganizationID: "org-home",
	}, slog.Default())
	if client == nil {
		t.Fatal("expected an OIDC client")
	}
	if got := client.Config().RequiredOrganizationID; got != "org-home" {
		t.Errorf("RequiredOrganizationID = %q", got)
	}
}
