// Package controlapi is the service's own small HTTP surface: is it up, and
// what has it done. Two endpoints, no state of its own, no way to make the
// service do anything.
//
// /healthz is always open so a container health check needs no secret.
// /v1/status carries the cost, so it can be put behind a bearer token.
package controlapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"
)

const shutdownGrace = 3 * time.Second

// Server serves the control API for one running service.
type Server struct {
	addr  string
	token string
	stats *Stats
	now   func() time.Time
}

// New builds the server. An empty token leaves /v1/status open.
func New(addr, token string, stats *Stats) *Server {
	return &Server{addr: addr, token: token, stats: stats, now: time.Now}
}

// Handler is the routing, exposed so tests can drive it without a socket.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorised(r) {
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(s.stats.snapshot(s.now()))
	})
	return mux
}

// Run serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		stop, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		_ = srv.Shutdown(stop)
	}()
	log.Printf("control api: listening on %s", s.addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// authorised checks the bearer token in constant time. No token configured
// means the endpoint is open, which is the right default for a service bound
// to localhost.
func (s *Server) authorised(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header[len(prefix):]), []byte(s.token)) == 1
}
