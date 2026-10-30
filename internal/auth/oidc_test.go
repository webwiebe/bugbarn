package auth

import "testing"

func TestOIDCClientAllowed(t *testing.T) {
	cli := NewOIDCClient(OIDCConfig{
		Issuer:                 "https://iam.example.com",
		ClientID:               "bugbarn",
		ClientSecret:           "sek",
		RedirectURL:            "https://bugbarn.example.com/api/v1/oidc/callback",
		RequiredOrganizationID: "org-home",
	})

	cases := []struct {
		name      string
		claims    Claims
		want      bool
		wrongOrgs bool
	}{
		{"owner", Claims{OrganizationID: "org-home", Roles: []string{"owner"}}, true, false},
		{"organization_admin", Claims{OrganizationID: "org-home", Roles: []string{"organization_admin"}}, true, false},
		{"operator", Claims{OrganizationID: "org-home", Roles: []string{"operator"}}, true, false},
		{"project group only", Claims{OrganizationID: "org-home", Groups: []string{"bugbarn-users"}}, false, false},
		{"no roles or groups in the organization", Claims{OrganizationID: "org-home"}, false, false},
		{"unrelated role", Claims{OrganizationID: "org-home", Roles: []string{"member"}}, false, false},
		{"no claims", Claims{}, false, true},
		{"owner of another organization", Claims{OrganizationID: "org-other", Roles: []string{"owner"}}, false, true},
		{"organization_admin of another organization", Claims{OrganizationID: "org-other", Roles: []string{"organization_admin"}}, false, true},
		{"operator of another organization", Claims{OrganizationID: "org-other", Roles: []string{"operator"}}, false, true},
		{"project group in another organization", Claims{OrganizationID: "org-other", Groups: []string{"bugbarn-users"}}, false, true},
		{"owner with no organization claim", Claims{Roles: []string{"owner"}}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cli.Allowed(tc.claims); got != tc.want {
				t.Errorf("Allowed(%+v) = %v, want %v", tc.claims, got, tc.want)
			}
			if got := cli.WrongOrganization(tc.claims); got != tc.wrongOrgs {
				t.Errorf("WrongOrganization(%+v) = %v, want %v", tc.claims, got, tc.wrongOrgs)
			}
		})
	}
}

func TestOIDCClientAllowedRefusesEverythingWithoutAnOrganization(t *testing.T) {
	cli := NewOIDCClient(OIDCConfig{
		Issuer:       "https://iam.example.com",
		ClientID:     "bugbarn",
		ClientSecret: "sek",
		RedirectURL:  "https://bugbarn.example.com/api/v1/oidc/callback",
	})
	for _, claims := range []Claims{
		{Roles: []string{"owner"}},
		{Groups: []string{"bugbarn-users"}},
		{OrganizationID: "org-home", Roles: []string{"owner"}},
	} {
		if cli.Allowed(claims) {
			t.Errorf("Allowed(%+v) = true with no organization configured", claims)
		}
		if cli.WrongOrganization(claims) {
			t.Errorf("WrongOrganization(%+v) = true with no organization configured", claims)
		}
	}
}

func TestOIDCConfigEnabled(t *testing.T) {
	cases := []struct {
		name string
		cfg  OIDCConfig
		want bool
	}{
		{"all set", OIDCConfig{Issuer: "i", ClientID: "c", ClientSecret: "s", RedirectURL: "r"}, true},
		{"missing issuer", OIDCConfig{ClientID: "c", ClientSecret: "s", RedirectURL: "r"}, false},
		{"missing client_id", OIDCConfig{Issuer: "i", ClientSecret: "s", RedirectURL: "r"}, false},
		{"missing secret", OIDCConfig{Issuer: "i", ClientID: "c", RedirectURL: "r"}, false},
		{"missing redirect", OIDCConfig{Issuer: "i", ClientID: "c", ClientSecret: "s"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Enabled(); got != tc.want {
				t.Errorf("Enabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOIDCClientPostLogoutRedirectURI(t *testing.T) {
	cases := []struct {
		name        string
		redirectURL string
		want        string
	}{
		{"https origin", "https://bugbarn.example.com/api/v1/oidc/callback", "https://bugbarn.example.com/api/v1/oidc/logged-out"},
		{"host with port", "http://localhost:8080/api/v1/oidc/callback", "http://localhost:8080/api/v1/oidc/logged-out"},
		{"unparseable", "://nope", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cli := NewOIDCClient(OIDCConfig{RedirectURL: tc.redirectURL})
			if got := cli.PostLogoutRedirectURI(); got != tc.want {
				t.Errorf("PostLogoutRedirectURI() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClaimsPreferredName(t *testing.T) {
	cases := []struct {
		claims Claims
		want   string
	}{
		{Claims{PreferredUsername: "alice", Email: "a@x"}, "alice"},
		{Claims{Email: "a@x"}, "a@x"},
		{Claims{Name: "Alice"}, "Alice"},
		{Claims{Subject: "sub-1"}, "sub-1"},
		{Claims{}, ""},
	}
	for _, tc := range cases {
		if got := tc.claims.PreferredName(); got != tc.want {
			t.Errorf("PreferredName(%+v) = %q, want %q", tc.claims, got, tc.want)
		}
	}
}
