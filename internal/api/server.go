package api

import (
	"net/http"

	"github.com/wiebe-xyz/bugbarn/internal/ingest"
)

type Server struct {
	mux *http.ServeMux
}

func NewServer(ingestHandler *ingest.Handler) *Server {
	mux := http.NewServeMux()
	mux.Handle("/api/v1/events", ingestHandler)
	return &Server{mux: mux}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
