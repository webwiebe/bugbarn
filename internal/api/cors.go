package api

import "net/http"

// ingestCORSHeaders is the wildcard-CORS header set for the unauthenticated
// browser ingest endpoints (events, logs): any origin may POST without
// credentials. The ingest-only key scope makes read access impossible.
const ingestCORSHeaders = "content-type, x-bugbarn-api-key, x-bugbarn-project"

// analyticsCORSHeaders is the wildcard-CORS header set for the analytics
// collection beacon, which carries no API key.
const analyticsCORSHeaders = "content-type, x-bugbarn-project"

// setWildcardCORS writes the Access-Control-Allow-* headers for an endpoint that
// accepts cross-origin requests from any origin without credentials.
func setWildcardCORS(w http.ResponseWriter, methods, headers string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", headers)
	w.Header().Set("Access-Control-Allow-Methods", methods)
}
