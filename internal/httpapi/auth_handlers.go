package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/dockside-gg/game-panel/internal/discord"
	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/internal/store"
)

type beginDiscordRequest struct {
	Purpose     string `json:"purpose"`
	ClaimToken  string `json:"claim_token,omitempty"`
	InviteToken string `json:"invite_token,omitempty"`
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.store.SetupStatus(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) beginDiscord(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != s.cfg.PublicURL.String() {
		writeProblem(w, r, errForbidden)
		return
	}
	var input beginDiscordRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Purpose == "" {
		input.Purpose = "login"
	}

	var inviteID *string
	switch input.Purpose {
	case "claim":
		if err := s.store.ValidateBootstrapToken(r.Context(), input.ClaimToken); err != nil {
			writeProblem(w, r, err)
			return
		}
	case "invite":
		resolved, err := s.store.ResolveInvite(r.Context(), input.InviteToken)
		if err != nil {
			writeProblem(w, r, err)
			return
		}
		inviteID = &resolved
	case "login":
	default:
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid OAuth purpose")))
		return
	}

	state, err := identity.Token(32)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if err := s.store.CreateOAuthState(r.Context(), input.Purpose, inviteID, state); err != nil {
		writeProblem(w, r, err)
		return
	}
	discordClient, err := s.discordClient(r)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthCookieName,
		Value:    state,
		Path:     "/api/v1/auth/discord/callback",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"authorization_url": discordClient.AuthorizationURL(state)})
}

func (s *Server) discordCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	cookie, err := r.Cookie(oauthCookieName)
	if err != nil || state == "" || code == "" ||
		subtle.ConstantTimeCompare([]byte(state), []byte(cookie.Value)) != 1 {
		s.redirectAuthError(w, r, "oauth_state")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthCookieName,
		Value:    "",
		Path:     "/api/v1/auth/discord/callback",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})

	oauthState, err := s.store.ConsumeOAuthState(r.Context(), state)
	if err != nil {
		s.redirectAuthError(w, r, "oauth_state")
		return
	}
	discordClient, err := s.discordClient(r)
	if err != nil {
		s.logger.Warn("load Discord OAuth settings failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		s.redirectAuthError(w, r, "discord_configuration")
		return
	}
	accessToken, err := discordClient.Exchange(r.Context(), code)
	if err != nil {
		s.logger.Warn("discord token exchange failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		s.redirectAuthError(w, r, "discord_exchange")
		return
	}
	discordUser, err := discordClient.CurrentUser(r.Context(), accessToken)
	if err != nil {
		s.logger.Warn("discord user lookup failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		s.redirectAuthError(w, r, "discord_identity")
		return
	}
	user, err := s.store.CompleteOAuth(r.Context(), oauthState, discordUser)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrMFARequired):
			s.redirectAuthError(w, r, "mfa_required")
		case errors.Is(err, store.ErrInviteInvalid):
			s.redirectAuthError(w, r, "invite_invalid")
		case errors.Is(err, store.ErrAlreadyClaimed):
			s.redirectAuthError(w, r, "already_claimed")
		default:
			s.logger.Warn("discord oauth completion failed", "request_id", requestIDFromContext(r.Context()), "error", err)
			s.redirectAuthError(w, r, "access_denied")
		}
		return
	}

	sessionToken, err := identity.Token(32)
	if err != nil {
		s.redirectAuthError(w, r, "session_failed")
		return
	}
	csrfToken, err := identity.Token(32)
	if err != nil {
		s.redirectAuthError(w, r, "session_failed")
		return
	}
	if err := s.store.CreateSession(
		r.Context(),
		user.ID,
		sessionToken,
		csrfToken,
		clientIP(r),
		r.UserAgent(),
		s.cfg.SessionIdle,
		s.cfg.SessionAbsolute,
	); err != nil {
		s.logger.Error("create session failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		s.redirectAuthError(w, r, "session_failed")
		return
	}
	s.setSessionCookies(w, sessionToken, csrfToken)

	destination := "/dashboard"
	if user.Status == "pending" {
		destination = "/pending"
	}
	http.Redirect(w, r, destination, http.StatusSeeOther)
}

func (s *Server) discordClient(r *http.Request) (*discord.Client, error) {
	publicURL, clientID, secret, err := s.store.DiscordCredentials(r.Context(), s.box)
	if err != nil {
		return nil, err
	}
	redirectURI := publicURL + "/api/v1/auth/discord/callback"
	return discord.New(clientID, secret, redirectURI), nil
}

func (s *Server) currentSession(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"user":                session.User,
		"idle_expires_at":     session.IdleExpiresAt,
		"absolute_expires_at": session.AbsoluteExpiresAt,
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.RevokeSession(r.Context(), session.ID); err != nil {
		writeProblem(w, r, err)
		return
	}
	s.clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) redirectAuthError(w http.ResponseWriter, r *http.Request, code string) {
	destination := "/login?error=" + url.QueryEscape(code)
	http.Redirect(w, r, destination, http.StatusSeeOther)
}

func (s *Server) setSessionCookies(w http.ResponseWriter, sessionToken, csrfToken string) {
	maxAge := int(s.cfg.SessionAbsolute.Seconds())
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionToken,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    csrfToken,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: false,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearSessionCookies(w http.ResponseWriter) {
	for _, cookie := range []http.Cookie{
		{Name: sessionCookieName, HttpOnly: true, SameSite: http.SameSiteLaxMode},
		{Name: csrfCookieName, HttpOnly: false, SameSite: http.SameSiteStrictMode},
	} {
		cookie.Value = ""
		cookie.Path = "/"
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(1, 0)
		cookie.Secure = s.cfg.SecureCookies
		http.SetCookie(w, &cookie)
	}
}
