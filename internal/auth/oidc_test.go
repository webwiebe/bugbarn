package auth

import "testing"

func TestOIDCClientAllowed(t *testing.T) {
	cli := NewOIDCClient(OIDCConfig{
		Issuer:        "https://iam.example.com",
		ClientID:      "bugbarn",
		ClientSecret:  "sek",
		RedirectURL:   "https://bugbarn.example.com/api/v1/oidc/callback",
		RequiredGroup: "bugbarn-users",
	})

	cases := []struct {
		name   string
		claims Claims
		want   bool
	}{
		{"in required group", Claims{Groups: []string{"bugbarn-users"}}, true},
		{"owner bypass", Claims{Roles: []string{"owner"}}, true},
		{"organization_admin bypass", Claims{Roles: []string{"organization_admin"}}, true},
		{"operator bypass", Claims{Roles: []string{"operator"}}, true},
		{"other group only", Claims{Groups: []string{"engineering"}}, false},
		{"no claims", Claims{}, false},
		{"unrelated role", Claims{Roles: []string{"member"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cli.Allowed(tc.claims); got != tc.want {
				t.Errorf("Allowed(%+v) = %v, want %v", tc.claims, got, tc.want)
			}
		})
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
