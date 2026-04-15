package auth

import "crypto/subtle"

const HeaderAPIKey = "x-bugbarn-api-key"

type Authorizer struct {
	apiKey string
}

func New(apiKey string) *Authorizer {
	return &Authorizer{apiKey: apiKey}
}

func (a *Authorizer) Enabled() bool {
	return a != nil && a.apiKey != ""
}

func (a *Authorizer) Valid(provided string) bool {
	if a == nil || a.apiKey == "" {
		return true
	}

	if len(provided) != len(a.apiKey) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(provided), []byte(a.apiKey)) == 1
}
