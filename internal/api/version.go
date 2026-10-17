package api

import "net/http"

// buildInfo is what GET /api/v1/version reports.
type buildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Built   string `json:"built"`
}

// SetBuildInfo records the running release: version is what the deploy stated
// (BUGBARN_VERSION), commit and built come from the binary. Deploy smokes
// compare them with what they just rolled out.
func (s *Server) SetBuildInfo(version, commit, built string) {
	s.build = buildInfo{Version: version, Commit: commit, Built: built}
}

// serveVersion answers on the writer and on every reader, since it is static
// process state. It lives under /api so the ingress sends it to the API pods; a
// bare /version would reach the SPA.
func (s *Server) serveVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.build)
}
