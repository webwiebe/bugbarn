package sessionstore

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/oidctest"
)

// TestDirectRefreshAppliesTheOrganizationCheck runs the real OIDC client, since
// the policy under test lives in OIDCClient.Allowed. IAMBarn's refresh grant
// sends no id_token, so the snapshot cases are the ones production takes.
func TestDirectRefreshAppliesTheOrganizationCheck(t *testing.T) {
	const homeOwner = `{"sub":"sub-1","organization_id":"` + oidctest.OrganizationID + `","roles":["owner"]}`
	cases := []struct {
		name     string
		snapshot string
		idToken  func(map[string]any) // nil sends no id_token
		revoked  bool
	}{
		{name: "snapshot of an owner in this organization", snapshot: homeOwner},
		{name: "snapshot of an owner in another organization", snapshot: `{"sub":"sub-1","organization_id":"org-other","roles":["owner"]}`, revoked: true},
		{name: "snapshot from before the organization claim", snapshot: `{"sub":"sub-1","roles":["owner"]}`, revoked: true},
		{name: "no snapshot", snapshot: "", revoked: true},
		{name: "unreadable snapshot", snapshot: "{", revoked: true},
		{name: "fresh id_token in this organization", snapshot: homeOwner, idToken: func(map[string]any) {}},
		{name: "fresh id_token in another organization", snapshot: homeOwner, idToken: func(c map[string]any) { c["organization_id"] = "org-other" }, revoked: true},
		{name: "fresh id_token with no organization", snapshot: homeOwner, idToken: func(c map[string]any) { delete(c, "organization_id") }, revoked: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idp, err := oidctest.New("bugbarn-test")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(idp.Close)
			idToken := ""
			if tc.idToken != nil {
				claims := idp.IDTokenClaims("sub-1", "sid-1", "", nil)
				tc.idToken(claims)
				idToken = idp.SignJWT(claims)
			}
			idp.SetTokenHandler(func(w http.ResponseWriter, _ *http.Request) {
				oidctest.WriteTokenResponse(w, idToken, "at-2", "rt-2", 900)
			})
			d, db := newDirect(t, auth.NewOIDCClient(auth.OIDCConfig{
				Issuer:                 idp.Issuer(),
				ClientID:               "bugbarn-test",
				ClientSecret:           "sek",
				RedirectURL:            "https://bugbarn.example.com/api/v1/oidc/callback",
				RequiredOrganizationID: oidctest.OrganizationID,
			}))
			ctx := context.Background()
			now := time.Now().UTC()
			ws := oidcSession("h1", now, now.Add(-time.Minute))
			ws.ClaimsJSON = tc.snapshot
			if err := db.InsertWebSession(ctx, ws); err != nil {
				t.Fatal(err)
			}

			_, err = d.Refresh(ctx, "h1")
			if tc.revoked {
				if !errors.Is(err, ErrRevoked) {
					t.Fatalf("Refresh = %v, want ErrRevoked", err)
				}
				if _, gerr := d.Get(ctx, "h1"); !errors.Is(gerr, ErrNotFound) {
					t.Error("row must be deleted when the session is refused")
				}
				return
			}
			if err != nil {
				t.Fatalf("Refresh = %v", err)
			}
			kept, gerr := d.Get(ctx, "h1")
			if gerr != nil {
				t.Fatal(gerr)
			}
			if kept.RefreshToken != "rt-2" {
				t.Errorf("rotation not persisted: %+v", kept)
			}
		})
	}
}
