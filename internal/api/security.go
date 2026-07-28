package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

const (
	browserSessionTTL = 8 * time.Hour
	secureCookieName  = "__Host-foxos_session"
	localCookieName   = "foxos_session"
	csrfHeaderName    = "X-FoxOS-CSRF"
)

type securityAuditStore interface {
	SaveAudit(context.Context, domain.AuditEvent) error
}

type requestSecurity struct {
	actor     string
	source    string
	host      string
	scheme    string
	csrfToken string
	expiresAt time.Time
}

type requestSecurityKey struct{}

type sessionRecord struct {
	csrfToken string
	expiresAt time.Time
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[[sha256.Size]byte]sessionRecord
	now      func() time.Time
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[[sha256.Size]byte]sessionRecord), now: time.Now}
}

func (s *sessionStore) create() (string, sessionRecord, error) {
	sessionToken, err := randomToken(32)
	if err != nil {
		return "", sessionRecord{}, err
	}
	csrfToken, err := randomToken(32)
	if err != nil {
		return "", sessionRecord{}, err
	}
	record := sessionRecord{csrfToken: csrfToken, expiresAt: s.now().UTC().Add(browserSessionTTL)}
	digest := sha256.Sum256([]byte(sessionToken))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(s.now().UTC())
	s.sessions[digest] = record
	return sessionToken, record, nil
}

func (s *sessionStore) get(token string) (sessionRecord, bool) {
	if len(token) != 64 {
		return sessionRecord{}, false
	}
	digest := sha256.Sum256([]byte(token))
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.purgeExpiredLocked(now)
	record, found := s.sessions[digest]
	if !found || !record.expiresAt.After(now) {
		delete(s.sessions, digest)
		return sessionRecord{}, false
	}
	return record, true
}

func (s *sessionStore) delete(token string) {
	digest := sha256.Sum256([]byte(token))
	s.mu.Lock()
	delete(s.sessions, digest)
	s.mu.Unlock()
}

func (s *sessionStore) purgeExpiredLocked(now time.Time) {
	for digest, record := range s.sessions {
		if !record.expiresAt.After(now) {
			delete(s.sessions, digest)
		}
	}
}

type rateEntry struct {
	count int
	reset time.Time
}

type fixedWindowLimiter struct {
	mu      sync.Mutex
	entries map[string]rateEntry
	now     func() time.Time
}

func newFixedWindowLimiter() *fixedWindowLimiter {
	return &fixedWindowLimiter{entries: make(map[string]rateEntry), now: time.Now}
}

func (l *fixedWindowLimiter) allow(key string, limit int, window time.Duration) (bool, time.Duration) {
	if limit < 1 || window <= 0 {
		return false, window
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now().UTC()
	entry, found := l.entries[key]
	if !found || !entry.reset.After(now) {
		if len(l.entries) >= 8192 {
			for candidate, current := range l.entries {
				if !current.reset.After(now) {
					delete(l.entries, candidate)
				}
			}
		}
		if len(l.entries) >= 8192 {
			return false, window
		}
		l.entries[key] = rateEntry{count: 1, reset: now.Add(window)}
		return true, window
	}
	if entry.count >= limit {
		return false, entry.reset.Sub(now)
	}
	entry.count++
	l.entries[key] = entry
	return true, entry.reset.Sub(now)
}

func randomToken(size int) (string, error) {
	if size < 16 || size > 128 {
		return "", errors.New("secure token size is invalid")
	}
	body := make([]byte, size)
	if _, err := rand.Read(body); err != nil {
		return "", err
	}
	return hex.EncodeToString(body), nil
}

func (s *Server) registerSession(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/session", http.HandlerFunc(s.createSession))
	mux.Handle("GET /api/v1/session", s.auth(http.HandlerFunc(s.readSession)))
	mux.Handle("DELETE /api/v1/session", s.auth(http.HandlerFunc(s.deleteSession)))
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	security := s.inspectRequest(r)
	s.clearForwardedHeaders(r)
	if s.requireSecure && !security.secure() {
		problemCode(w, http.StatusUpgradeRequired, "https_required")
		return
	}
	if !sameOriginRequest(r, security) {
		s.recordSecurityEvent(r.Context(), security, "security.session_rejected", "origin_rejected", domain.AuditFailed)
		problemCode(w, http.StatusForbidden, "origin_rejected")
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		problemCode(w, http.StatusUnsupportedMediaType, "json_required")
		return
	}
	if allowed, retry := s.limiter.allow("session-login|"+security.source, 5, time.Minute); !allowed {
		s.rateLimited(w, r.Context(), security, retry, "session_login")
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	if err := decodeLimit(r, &input, 4<<10); err != nil {
		problem(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	candidate := sha256.Sum256([]byte(input.Token))
	if len(input.Token) < 32 || len(input.Token) > 4096 || subtle.ConstantTimeCompare(candidate[:], s.token[:]) != 1 {
		s.recordSecurityEvent(r.Context(), security, "security.authentication_failed", "invalid_api_token", domain.AuditFailed)
		w.Header().Set("WWW-Authenticate", "Bearer")
		problemCode(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	sessionToken, record, err := s.sessions.create()
	if err != nil {
		problemCode(w, http.StatusInternalServerError, "session_unavailable")
		return
	}
	http.SetCookie(w, sessionCookie(sessionToken, record.expiresAt, security.secure()))
	security.actor = "browser-session"
	s.recordSecurityEvent(r.Context(), security, "security.session_created", "browser_session", domain.AuditSucceeded)
	writeJSON(w, http.StatusCreated, map[string]any{
		"csrfToken": record.csrfToken,
		"expiresAt": record.expiresAt.Format(time.RFC3339Nano),
	})
}

func (s *Server) readSession(w http.ResponseWriter, r *http.Request) {
	security := requestSecurityFromContext(r.Context())
	if security.actor != "browser-session" {
		problemCode(w, http.StatusConflict, "browser_session_required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"csrfToken": security.csrfToken,
		"expiresAt": security.expiresAt.Format(time.RFC3339Nano),
	})
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	security := requestSecurityFromContext(r.Context())
	name := cookieName(security.secure())
	if cookie, err := r.Cookie(name); err == nil {
		s.sessions.delete(cookie.Value)
	}
	http.SetCookie(w, expiredSessionCookie(security.secure()))
	s.recordSecurityEvent(r.Context(), security, "security.session_deleted", "browser_session", domain.AuditSucceeded)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		security := s.inspectRequest(r)
		s.clearForwardedHeaders(r)
		if s.requireSecure && !security.secure() {
			problemCode(w, http.StatusUpgradeRequired, "https_required")
			return
		}
		class, limit, window := requestRateClass(r)
		if allowed, retry := s.limiter.allow(class+"|"+security.source, limit, window); !allowed {
			s.rateLimited(w, r.Context(), security, retry, class)
			return
		}
		if value := r.Header.Get("Authorization"); value != "" {
			const prefix = "Bearer "
			raw := strings.TrimPrefix(value, prefix)
			candidate := sha256.Sum256([]byte(raw))
			if !strings.HasPrefix(value, prefix) || len(raw) < 32 || len(raw) > 4096 || strings.TrimSpace(raw) != raw || subtle.ConstantTimeCompare(candidate[:], s.token[:]) != 1 {
				s.authenticationFailed(w, r.Context(), security, "invalid_bearer")
				return
			}
			security.actor = "api-token"
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestSecurityKey{}, security)))
			return
		}
		cookie, err := r.Cookie(cookieName(security.secure()))
		if err != nil {
			s.authenticationFailed(w, r.Context(), security, "missing_credentials")
			return
		}
		record, found := s.sessions.get(cookie.Value)
		if !found {
			s.authenticationFailed(w, r.Context(), security, "invalid_session")
			return
		}
		security.actor = "browser-session"
		security.csrfToken = record.csrfToken
		security.expiresAt = record.expiresAt
		if requestChangesState(r.Method) {
			if !sameOriginRequest(r, security) || !matchingCSRF(r.Header.Get(csrfHeaderName), record.csrfToken) {
				s.recordSecurityEvent(r.Context(), security, "security.csrf_rejected", "csrf_rejected", domain.AuditFailed)
				problemCode(w, http.StatusForbidden, "csrf_rejected")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestSecurityKey{}, security)))
	})
}

func (s *Server) authenticationFailed(w http.ResponseWriter, ctx context.Context, security requestSecurity, reason string) {
	s.recordSecurityEvent(ctx, security, "security.authentication_failed", reason, domain.AuditFailed)
	w.Header().Set("WWW-Authenticate", "Bearer")
	problemCode(w, http.StatusUnauthorized, "unauthorized")
}

func (s *Server) rateLimited(w http.ResponseWriter, ctx context.Context, security requestSecurity, retry time.Duration, class string) {
	seconds := int(retry.Round(time.Second) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	s.recordSecurityEvent(ctx, security, "security.rate_limited", class, domain.AuditFailed)
	problemCode(w, http.StatusTooManyRequests, "rate_limited")
}

func (s *Server) recordSecurityEvent(ctx context.Context, security requestSecurity, action, reason string, outcome domain.AuditOutcome) {
	if s.audit == nil {
		return
	}
	if allowed, _ := s.limiter.allow("audit|"+security.source+"|"+action+"|"+reason, 1, time.Minute); !allowed {
		return
	}
	id, err := randomToken(16)
	if err != nil {
		return
	}
	actor := security.actor
	if actor == "" {
		actor = "unauthenticated"
	}
	_ = s.audit.SaveAudit(ctx, domain.AuditEvent{
		ID:       "security-" + id,
		Action:   action,
		TargetID: reason,
		Outcome:  outcome,
		Details: map[string]any{
			"actor":  actor,
			"source": security.source,
			"reason": reason,
		},
	})
}

func (s *Server) inspectRequest(r *http.Request) requestSecurity {
	result := requestSecurity{source: remoteHost(r.RemoteAddr), host: r.Host, scheme: "http"}
	if r.TLS != nil {
		result.scheme = "https"
	}
	if !s.requestFromTrustedProxy(r) {
		return result
	}
	result.scheme = "https"
	if forwarded := singleForwardedValue(r.Header.Get("X-Forwarded-For")); net.ParseIP(forwarded) != nil {
		result.source = forwarded
	}
	if forwarded := singleForwardedValue(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
		result.host = forwarded
	}
	return result
}

func (s *Server) requestFromTrustedProxy(r *http.Request) bool {
	if s.trustedProxyHeader == "" || r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	ip := net.ParseIP(remoteHost(r.RemoteAddr))
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	candidate := sha256.Sum256([]byte(r.Header.Get(s.trustedProxyHeader)))
	return subtle.ConstantTimeCompare(candidate[:], s.trustedProxyToken[:]) == 1
}

func (s *Server) clearForwardedHeaders(r *http.Request) {
	for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", s.trustedProxyHeader} {
		if name != "" {
			r.Header.Del(name)
		}
	}
}

func (security requestSecurity) secure() bool { return security.scheme == "https" }

func requestSecurityFromContext(ctx context.Context) requestSecurity {
	security, _ := ctx.Value(requestSecurityKey{}).(requestSecurity)
	return security
}

func requestRateClass(r *http.Request) (string, int, time.Duration) {
	if !requestChangesState(r.Method) {
		return "read", 300, time.Minute
	}
	path := r.URL.Path
	if strings.Contains(path, "/restore") || strings.Contains(path, "/execute") || strings.Contains(path, "/config/apply") || strings.Contains(path, "/system/upgrade") {
		return "high-risk", 10, 5 * time.Minute
	}
	if strings.Contains(path, "/subscriptions") || strings.Contains(path, "/nodes/import") {
		return "subscription", 20, time.Minute
	}
	return "write", 60, time.Minute
}

func requestChangesState(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func sameOriginRequest(r *http.Request, security requestSecurity) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return false
	}
	if parsed.Scheme != security.scheme || !strings.EqualFold(parsed.Host, security.host) {
		return false
	}
	if site := strings.TrimSpace(strings.ToLower(r.Header.Get("Sec-Fetch-Site"))); site != "" && site != "same-origin" {
		return false
	}
	return true
}

func matchingCSRF(candidate, expected string) bool {
	if len(candidate) != len(expected) || len(expected) < 32 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) == 1
}

func sessionCookie(value string, expiresAt time.Time, secure bool) *http.Cookie {
	// #nosec G124 -- production requires the HTTPS gateway and always passes secure=true;
	// the non-Secure cookie has a different name and exists only for explicit loopback development.
	return &http.Cookie{
		Name:     cookieName(secure),
		Value:    value,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Round(time.Second) / time.Second),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

func expiredSessionCookie(secure bool) *http.Cookie {
	// #nosec G124 -- this expires the same production-Secure or loopback-development cookie.
	return &http.Cookie{
		Name:     cookieName(secure),
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

func cookieName(secure bool) string {
	if secure {
		return secureCookieName
	}
	return localCookieName
}

func remoteHost(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		return host
	}
	return address
}

func singleForwardedValue(value string) string {
	if value == "" || strings.Contains(value, ",") {
		return ""
	}
	return strings.TrimSpace(value)
}
