package httpapi

import (
	"bufio"
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/internal/secure"
	"github.com/dockside-gg/game-panel/internal/store"
	"github.com/go-chi/chi/v5"
)

var (
	errForbidden  = errors.New("forbidden")
	errBadRequest = errors.New("bad request")
)

func (s *Server) middleware(next http.Handler) http.Handler {
	return s.recoverer(s.requestID(s.securityHeaders(s.accessLog(next))))
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, err := identity.Token(16)
		if err != nil {
			http.Error(w, "could not generate request id", http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDContextKey, requestID)))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("http panic", "request_id", requestIDFromContext(r.Context()), "panic", recovered, "stack", string(debug.Stack()))
				writeProblem(w, r, errors.New("panic"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' https://cdn.discordapp.com data:; style-src 'self' 'unsafe-inline'; script-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.LogAttrs(
			r.Context(),
			slog.LevelInfo,
			"http request",
			slog.String("request_id", requestIDFromContext(r.Context())),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", recorder.status),
			slog.Duration("duration", time.Since(started)),
		)
	})
}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			writeProblem(w, r, store.ErrUnauthorized)
			return
		}
		session, err := s.store.SessionByToken(r.Context(), cookie.Value, s.cfg.SessionIdle)
		if err != nil {
			s.clearSessionCookies(w)
			writeProblem(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionContextKey, session)))
	})
}

func (s *Server) requireActive(next http.Handler) http.Handler {
	return s.requireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, _ := sessionFromContext(r.Context())
		if session.User.Status != "active" {
			writeProblem(w, r, errForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (s *Server) requireOwner(next http.Handler) http.Handler {
	return s.requireActive(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, _ := sessionFromContext(r.Context())
		if session.User.PanelRole != "owner" {
			writeProblem(w, r, errForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (s *Server) requireAdministrator(next http.Handler) http.Handler {
	return s.requireActive(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, _ := sessionFromContext(r.Context())
		if session.User.PanelRole != "owner" && session.User.PanelRole != "administrator" {
			writeProblem(w, r, errForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (s *Server) requireServerPermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, ok := sessionFromContext(r.Context())
			if !ok {
				writeProblem(w, r, store.ErrUnauthorized)
				return
			}
			if session.User.PanelRole == "owner" || session.User.PanelRole == "administrator" {
				next.ServeHTTP(w, r)
				return
			}
			serverID := chi.URLParam(r, "serverID")
			allowed, err := s.store.UserHasServerPermission(
				r.Context(), session.User.ID, serverID, permission,
			)
			if err != nil {
				writeProblem(w, r, err)
				return
			}
			if !allowed {
				writeProblem(w, r, errForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		session, ok := sessionFromContext(r.Context())
		if !ok {
			writeProblem(w, r, store.ErrUnauthorized)
			return
		}
		token := r.Header.Get("X-CSRF-Token")
		origin := r.Header.Get("Origin")
		if token == "" || origin != s.cfg.PublicURL.String() {
			writeProblem(w, r, errForbidden)
			return
		}
		actual := secure.Hash(token)
		if subtle.ConstantTimeCompare([]byte(actual), []byte(session.CSRFHash)) != 1 {
			writeProblem(w, r, errForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return net.ParseIP(r.RemoteAddr)
	}
	return net.ParseIP(host)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking not supported")
	}
	return hijacker.Hijack()
}
