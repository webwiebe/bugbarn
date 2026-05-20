package api

import "net/http"

// themeManifest mirrors the iambarn relying-party theme manifest schema.
// See: https://iam.wiebe.xyz/.well-known/iambarn-theme.json
type themeManifest struct {
	Name            string `json:"name"`
	LogoURL         string `json:"logo_url"`
	PrimaryColor    string `json:"primary_color"`
	BackgroundColor string `json:"background_color"`
	CardColor       string `json:"card_color"`
	BodyTextColor   string `json:"body_text_color"`
	SupportURL      string `json:"support_url"`
	Locale          string `json:"locale"`
}

// bugbarnThemeManifest reflects the BugBarn brand as defined in
// web/styles.css (--bg, --panel, --accent, --text) and web/manifest.json.
var bugbarnThemeManifest = themeManifest{
	Name:            "BugBarn",
	LogoURL:         "https://bugbarn.wiebe.xyz/icons/icon-512.png",
	PrimaryColor:    "#a6e22e",
	BackgroundColor: "#171812",
	CardColor:       "#24251c",
	BodyTextColor:   "#f8f8f2",
	SupportURL:      "https://bugbarn.wiebe.xyz/",
	Locale:          "en",
}

// serveThemeManifest serves the iambarn relying-party theme manifest used by
// iambarn to skin its login page when a user is redirected here for OIDC.
func (s *Server) serveThemeManifest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, bugbarnThemeManifest)
}
