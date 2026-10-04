package api

import "net/http"

// themeManifest mirrors the iambarn relying-party theme manifest schema
// (iambarn internal/theme.Manifest), all 13 fields.
// See: https://iam.wiebe.xyz/.well-known/iambarn-theme.json
type themeManifest struct {
	Name             string        `json:"name"`
	LogoURL          string        `json:"logo_url"`
	PrimaryColor     string        `json:"primary_color"`
	BackgroundColor  string        `json:"background_color"`
	CardColor        string        `json:"card_color"`
	BodyTextColor    string        `json:"body_text_color"`
	SupportURL       string        `json:"support_url"`
	Locale           string        `json:"locale"`
	DefaultLocale    string        `json:"default_locale,omitempty"`
	SupportedLocales []string      `json:"supported_locales,omitempty"`
	FromAddress      string        `json:"from_address,omitempty"`
	FromName         string        `json:"from_name,omitempty"`
	Dark             *themePalette `json:"dark,omitempty"`
}

// themePalette is the color-only palette iambarn applies under
// prefers-color-scheme: dark.
type themePalette struct {
	PrimaryColor    string `json:"primary_color"`
	BackgroundColor string `json:"background_color"`
	CardColor       string `json:"card_color"`
	BodyTextColor   string `json:"body_text_color"`
}

// bugbarnPalette reflects the BugBarn brand as defined in web/styles.css
// (--accent, --bg, --panel, --text). The dashboard is dark-only, so the same
// palette serves both color schemes.
var bugbarnPalette = themePalette{
	PrimaryColor:    "#a6e22e",
	BackgroundColor: "#171812",
	CardColor:       "#24251c",
	BodyTextColor:   "#f8f8f2",
}

// bugbarnThemeManifest is the BugBarn brand for iambarn's login page and
// transactional mail. FromAddress is the mailbox production already sends
// notifications from; iambarn refuses senders outside the tenant's own zone,
// and bugbarn.wiebe.xyz lives in wiebe.xyz.
var bugbarnThemeManifest = themeManifest{
	Name:             "BugBarn",
	LogoURL:          "https://bugbarn.wiebe.xyz/app/icons/icon-512.png",
	PrimaryColor:     bugbarnPalette.PrimaryColor,
	BackgroundColor:  bugbarnPalette.BackgroundColor,
	CardColor:        bugbarnPalette.CardColor,
	BodyTextColor:    bugbarnPalette.BodyTextColor,
	SupportURL:       "https://bugbarn.wiebe.xyz/",
	Locale:           "en",
	DefaultLocale:    "en",
	SupportedLocales: []string{"en"},
	FromAddress:      "bugbarn@wiebe.xyz",
	FromName:         "BugBarn",
	Dark:             &bugbarnPalette,
}

// serveThemeManifest serves the iambarn relying-party theme manifest used by
// iambarn to skin its login page when a user is redirected here for OIDC.
func (s *Server) serveThemeManifest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, bugbarnThemeManifest)
}
