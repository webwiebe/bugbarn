package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// ExchangeResult holds the parsed claims and the raw OIDC tokens from the
// authorization-code exchange.
type ExchangeResult struct {
	Claims       Claims
	IDToken      string // raw id_token; kept for id_token_hint at logout
	AccessToken  string
	RefreshToken string    // empty if the client/grant did not include offline_access
	ExpiresAt    time.Time // zero if the token response omitted expires_in
}

// ExchangeFull swaps an authorization code for tokens, verifies the ID token's
// signature + audience + nonce, and returns both the parsed claims (including
// the IdP session id `sid`) and the raw tokens in one call. verifier is the
// PKCE code_verifier that produced the challenge sent at authorize time.
func (c *OIDCClient) ExchangeFull(ctx context.Context, code, nonce, verifier string) (ExchangeResult, error) {
	if err := c.ensureReady(ctx); err != nil {
		return ExchangeResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var opts []oauth2.AuthCodeOption
	if verifier != "" {
		opts = append(opts, oauth2.VerifierOption(verifier))
	}
	tok, err := c.oauth.Exchange(ctx, code, opts...)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("oidc: token exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return ExchangeResult{}, errors.New("oidc: token response missing id_token")
	}
	idToken, err := c.verifier.Verify(ctx, rawID)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("oidc: verify id_token: %w", err)
	}
	if idToken.Nonce != nonce {
		return ExchangeResult{}, errors.New("oidc: nonce mismatch")
	}
	var claims Claims
	if err := idToken.Claims(&claims); err != nil {
		return ExchangeResult{}, fmt.Errorf("oidc: decode claims: %w", err)
	}
	return ExchangeResult{
		Claims:       claims,
		IDToken:      rawID,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.Expiry,
	}, nil
}

// ErrRefreshInvalid indicates iambarn rejected the refresh_token outright
// (invalid_grant: revoked, expired, already-rotated/replayed, or the user was
// suspended). The caller must not retry the same token — it is dead — and
// should kill the local session.
var ErrRefreshInvalid = errors.New("oidc: refresh token invalid")

// RefreshedTokens holds the renewed access/refresh token pair from a
// refresh_token grant. Iambarn rotates the refresh token on every use, so
// RefreshToken here always replaces whatever was previously stored.
type RefreshedTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	// IDToken and Claims are set when the token response included a fresh
	// id_token (iambarn does on refresh). Claims lets the caller re-snapshot
	// groups/roles so central role changes propagate on the next refresh
	// instead of the next login.
	IDToken string
	Claims  *Claims
}

// Refresh exchanges a refresh_token for a new access/refresh token pair.
//
// Refresh tokens are single-use: iambarn invalidates the one sent here the
// moment it issues the replacement, and a replay revokes the whole token
// family. Callers MUST serialize refreshes per session (singleflight); in the
// CQRS deployment refresh therefore only ever executes on the writer.
func (c *OIDCClient) Refresh(ctx context.Context, refreshToken string) (RefreshedTokens, error) {
	if err := c.ensureReady(ctx); err != nil {
		return RefreshedTokens{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	tok, err := c.oauth.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
	if err != nil {
		var retrieveErr *oauth2.RetrieveError
		if errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "invalid_grant" {
			return RefreshedTokens{}, ErrRefreshInvalid
		}
		return RefreshedTokens{}, fmt.Errorf("oidc: refresh token: %w", err)
	}
	// golang.org/x/oauth2 falls back to the refresh_token we sent when the
	// response omits one, so tok.RefreshToken is never empty on success.
	refreshed := RefreshedTokens{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.Expiry,
	}
	// A refresh response may carry a fresh id_token with up-to-date claims.
	// Verify it like the login-time one (signature/issuer/audience), except
	// the nonce — refresh grants have no browser round-trip to bind one to.
	if rawID, ok := tok.Extra("id_token").(string); ok && rawID != "" {
		if idToken, verr := c.verifier.Verify(ctx, rawID); verr == nil {
			var claims Claims
			if cerr := idToken.Claims(&claims); cerr == nil {
				refreshed.IDToken = rawID
				refreshed.Claims = &claims
			}
		}
	}
	return refreshed, nil
}

// RevokeRefreshToken revokes a refresh token at the issuer's revocation
// endpoint (RFC 7009), authenticating as the client. Used best-effort at
// logout so the token family dies server-side instead of merely being
// forgotten locally.
func (c *OIDCClient) RevokeRefreshToken(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	if err := c.ensureReady(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	form := url.Values{}
	form.Set("token", refreshToken)
	form.Set("token_type_hint", "refresh_token")
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.revocationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("oidc: revoke refresh token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("oidc: revoke refresh token: status %d", resp.StatusCode)
	}
	return nil
}

// backchannelLogoutEvent is the member the `events` claim of a logout token
// must contain (OIDC Back-Channel Logout 1.0 §2.4).
const backchannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"

// logoutTokenMaxAge bounds how old a logout token's iat may be. Tokens are
// minted per delivery attempt, so anything older is a replay.
const logoutTokenMaxAge = 2 * time.Minute

// LogoutClaims carries the session-targeting claims of a validated
// back-channel logout token. At least one of Subject/SessionID is non-empty.
type LogoutClaims struct {
	Subject   string
	SessionID string
}

// VerifyLogoutToken validates a back-channel logout token per OIDC
// Back-Channel Logout 1.0: signature + issuer + audience (= our client_id)
// via the issuer's JWKS, iat within logoutTokenMaxAge, the mandatory
// backchannel-logout `events` member, the mandatory absence of `nonce`, and
// the presence of at least one of sub/sid.
func (c *OIDCClient) VerifyLogoutToken(ctx context.Context, raw string) (LogoutClaims, error) {
	if err := c.ensureReady(ctx); err != nil {
		return LogoutClaims{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	tok, err := c.verifier.Verify(ctx, raw)
	if err != nil {
		return LogoutClaims{}, fmt.Errorf("oidc: verify logout token: %w", err)
	}
	var claims struct {
		Sub    string                     `json:"sub"`
		Sid    string                     `json:"sid"`
		Iat    int64                      `json:"iat"`
		Nonce  *string                    `json:"nonce"`
		Events map[string]json.RawMessage `json:"events"`
	}
	if err := tok.Claims(&claims); err != nil {
		return LogoutClaims{}, fmt.Errorf("oidc: decode logout token claims: %w", err)
	}
	if claims.Nonce != nil {
		// The spec REQUIRES rejecting logout tokens with a nonce — it is what
		// distinguishes them from ID tokens, blocking cross-protocol replay.
		return LogoutClaims{}, errors.New("oidc: logout token must not contain nonce")
	}
	if _, ok := claims.Events[backchannelLogoutEvent]; !ok {
		return LogoutClaims{}, errors.New("oidc: logout token missing backchannel-logout event")
	}
	iat := time.Unix(claims.Iat, 0)
	now := time.Now()
	if claims.Iat == 0 || now.Sub(iat) > logoutTokenMaxAge || iat.Sub(now) > logoutTokenMaxAge {
		return LogoutClaims{}, errors.New("oidc: logout token iat outside acceptance window")
	}
	if claims.Sub == "" && claims.Sid == "" {
		return LogoutClaims{}, errors.New("oidc: logout token has neither sub nor sid")
	}
	return LogoutClaims{Subject: claims.Sub, SessionID: claims.Sid}, nil
}
