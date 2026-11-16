package config

import "testing"

func TestLoadReadsOIDCOrganizationID(t *testing.T) {
	t.Setenv("BUGBARN_OIDC_ORGANIZATION_ID", "org-home")
	if got := Load().OIDCOrganizationID; got != "org-home" {
		t.Errorf("OIDCOrganizationID = %q", got)
	}
}
