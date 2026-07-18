package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// CookieName stores the shared token for browser navigation / same-origin WS.
	CookieName = "gaderno_token"
	// ticketTTL is the lifetime of a one-shot WS ticket.
	ticketTTL = 2 * time.Minute
	// ticketBytes is the entropy of minted tickets.
	ticketBytes = 24
)

// Gate enforces the optional shared-token access model (SPEC §5).
// When Token is empty, Middleware is a no-op.
type Gate struct {
	Token string

	mu      sync.Mutex
	tickets map[string]time.Time
}

// New returns a Gate. Empty token disables enforcement.
func New(token string) *Gate {
	return &Gate{
		Token:   strings.TrimSpace(token),
		tickets: make(map[string]time.Time),
	}
}

// Enabled reports whether a shared token is configured.
func (g *Gate) Enabled() bool {
	return g != nil && g.Token != ""
}

// ValidToken reports whether candidate matches the configured shared token.
func (g *Gate) ValidToken(candidate string) bool {
	if !g.Enabled() {
		return true
	}
	a := []byte(g.Token)
	b := []byte(candidate)
	if len(a) != len(b) {
		// subtle.ConstantTimeCompare panics on length mismatch; still avoid
		// leaking length via early return timing on equal-length probes only.
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

// MintTicket issues a short-lived, single-use ticket for WebSocket upgrade
// (browsers cannot set Authorization on WS).
func (g *Gate) MintTicket() (string, time.Duration, error) {
	if !g.Enabled() {
		return "", 0, nil
	}
	buf := make([]byte, ticketBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", 0, err
	}
	id := hex.EncodeToString(buf)
	exp := time.Now().Add(ticketTTL)
	g.mu.Lock()
	g.purgeExpiredLocked(time.Now())
	g.tickets[id] = exp
	g.mu.Unlock()
	return id, ticketTTL, nil
}

// ConsumeTicket validates and removes a one-shot ticket.
func (g *Gate) ConsumeTicket(id string) bool {
	if !g.Enabled() {
		return true
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.purgeExpiredLocked(now)
	exp, ok := g.tickets[id]
	if !ok {
		return false
	}
	delete(g.tickets, id)
	return now.Before(exp)
}

func (g *Gate) purgeExpiredLocked(now time.Time) {
	for id, exp := range g.tickets {
		if !now.Before(exp) {
			delete(g.tickets, id)
		}
	}
}

// TokenFromRequest extracts a bearer/shared token from Authorization, cookie,
// or (bootstrap only) the token query parameter. It does not accept tickets.
func (g *Gate) TokenFromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		const p = "Bearer "
		if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
			return strings.TrimSpace(h[len(p):])
		}
	}
	if c, err := r.Cookie(CookieName); err == nil {
		return c.Value
	}
	return ""
}

// Authorized reports whether r carries a valid shared token or (for WS) ticket.
func (g *Gate) Authorized(r *http.Request) bool {
	if !g.Enabled() {
		return true
	}
	if g.ValidToken(g.TokenFromRequest(r)) {
		return true
	}
	// One-shot ticket for WebSocket clients that cannot set headers.
	if t := r.URL.Query().Get("ticket"); t != "" {
		return g.ConsumeTicket(t)
	}
	return false
}

// SetCookie writes an HttpOnly session cookie holding the shared token.
func (g *Gate) SetCookie(w http.ResponseWriter, r *http.Request) {
	if !g.Enabled() {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    g.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		// Session cookie: cleared when browser closes.
	})
}

// ClearCookie expires the auth cookie.
func (g *Gate) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		SameSite: http.SameSiteStrictMode,
	})
}

func isPublicPath(path string) bool {
	if path == "/healthz" {
		return true
	}
	if strings.HasPrefix(path, "/static/") {
		return true
	}
	// Ticket mint requires auth; listed here only for clarity — not public.
	return false
}

func wantsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

// Middleware enforces the shared token when configured.
// Bootstrap: ?token=SECRET sets the cookie and redirects with the query stripped.
func (g *Gate) Middleware(next http.Handler) http.Handler {
	if !g.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bootstrap via query (browser UX). Prefer header/cookie thereafter.
		if q := r.URL.Query().Get("token"); q != "" {
			if !g.ValidToken(q) {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			g.SetCookie(w, r)
			u := *r.URL
			vals := u.Query()
			vals.Del("token")
			u.RawQuery = vals.Encode()
			http.Redirect(w, r, u.String(), http.StatusFound)
			return
		}

		if isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		if g.Authorized(r) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("WWW-Authenticate", `Bearer realm="gaderno"`)
		if wantsHTML(r) && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(authPageHTML))
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// RegisterTicketRoute mounts POST /api/ws-ticket (auth required via Middleware).
func (g *Gate) RegisterTicketRoute(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/ws-ticket", func(w http.ResponseWriter, r *http.Request) {
		if !g.Enabled() {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ticket":""}` + "\n"))
			return
		}
		// Middleware already authorized; re-check for defense in depth.
		if !g.ValidToken(g.TokenFromRequest(r)) {
			// Tickets themselves must not mint further tickets.
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, ttl, err := g.MintTicket()
		if err != nil {
			http.Error(w, "mint failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ticket":     id,
			"expires_in": int(ttl.Seconds()),
		})
	})
}

const authPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>gaderno — token required</title>
  <style>
    body{font-family:system-ui,sans-serif;max-width:24rem;margin:4rem auto;padding:0 1rem;color:#111}
    label{display:block;font-size:.875rem;margin-bottom:.35rem}
    input{width:100%;padding:.5rem .6rem;font:inherit;box-sizing:border-box}
    button{margin-top:.75rem;padding:.45rem .9rem;font:inherit;cursor:pointer}
    p{color:#555;font-size:.875rem;line-height:1.4}
  </style>
</head>
<body>
  <h1>Token required</h1>
  <p>This gaderno instance has a shared access token. Possession grants full read, write, and kernel execute as the server OS user.</p>
  <form method="get" action="">
    <label for="token">Access token</label>
    <input id="token" name="token" type="password" autocomplete="current-password" required autofocus>
    <button type="submit">Continue</button>
  </form>
</body>
</html>
`
