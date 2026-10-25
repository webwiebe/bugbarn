package main

import (
	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/config"
)

// newUserAuthenticator builds the web login from config: the admin login plus
// the optional smoke login (BUGBARN_SMOKE_USERNAME / BUGBARN_SMOKE_PASSWORD_HASH).
func newUserAuthenticator(cfg config.Config) (*auth.UserAuthenticator, error) {
	users, err := auth.NewUserAuthenticator(cfg.AdminUsername, cfg.AdminPassword, cfg.AdminPasswordBcrypt)
	if err != nil {
		return nil, err
	}
	return users.WithSmokeLogin(cfg.SmokeUsername, cfg.SmokePasswordHash)
}
