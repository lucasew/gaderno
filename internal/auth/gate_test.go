package auth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsLoopbackListen(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"localhost:8080", true},
		{"[::1]:8080", true},
		{"0.0.0.0:8080", false},
		{":8080", false},
		{"192.168.1.5:8080", false},
		{"[::]:8080", false},
	}
	for _, tc := range cases {
		if got := IsLoopbackListen(tc.addr); got != tc.want {
			t.Errorf("IsLoopbackListen(%q)=%v want %v", tc.addr, got, tc.want)
		}
	}
}

func TestCheckBind(t *testing.T) {
	if err := CheckBind("127.0.0.1:8080", "", false); err != nil {
		t.Fatalf("loopback: %v", err)
	}
	if err := CheckBind("0.0.0.0:8080", "secret", false); err != nil {
		t.Fatalf("token on public: %v", err)
	}
	if err := CheckBind("0.0.0.0:8080", "", true); err != nil {
		t.Fatalf("i-understand: %v", err)
	}
	if err := CheckBind("0.0.0.0:8080", "", false); err == nil {
		t.Fatal("expected refuse without token")
	}
}

func TestGateDisabled(t *testing.T) {
	g := New("")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /secret", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := g.Middleware(mux)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/secret", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("disabled gate code=%d", rr.Code)
	}
}

func TestGateBearerAndCookie(t *testing.T) {
	g := New("s3cret")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/x", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	h := g.Middleware(mux)

	// No creds → 401
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/x", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no auth: %d", rr.Code)
	}

	// Bearer
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("bearer: %d body=%s", rr.Code, rr.Body.String())
	}

	// Wrong bearer
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("bad bearer: %d", rr.Code)
	}

	// Bootstrap query → cookie + redirect
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/x?token=s3cret", nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("bootstrap: %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/api/x" {
		t.Fatalf("location=%q", loc)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != CookieName {
		t.Fatalf("expected cookie, got %#v", cookies)
	}

	// Cookie auth
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.AddCookie(cookies[0])
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("cookie: %d", rr.Code)
	}
}

func TestGatePublicHealthz(t *testing.T) {
	g := New("s3cret")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	h := g.Middleware(mux)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz: %d", rr.Code)
	}
}

func TestTicketSingleUse(t *testing.T) {
	g := New("s3cret")
	id, ttl, err := g.MintTicket()
	if err != nil || id == "" || ttl <= 0 {
		t.Fatalf("mint: id=%q ttl=%v err=%v", id, ttl, err)
	}
	if !g.ConsumeTicket(id) {
		t.Fatal("first consume failed")
	}
	if g.ConsumeTicket(id) {
		t.Fatal("ticket should be single-use")
	}
}

func TestTicketOnWSPath(t *testing.T) {
	g := New("s3cret")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/notebooks/{path...}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("upgraded"))
	})
	g.RegisterTicketRoute(mux)
	h := g.Middleware(mux)

	// Mint via bearer
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/ws-ticket", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("mint http: %d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil || body.Ticket == "" {
		t.Fatalf("mint body: %v %q", err, rr.Body.String())
	}

	// Use ticket once
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ws/notebooks/foo.ipynb?ticket="+body.Ticket, nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "upgraded") {
		t.Fatalf("ws ticket: %d %s", rr.Code, rr.Body.String())
	}

	// Reuse fails
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ws/notebooks/foo.ipynb?ticket="+body.Ticket, nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("reuse: %d", rr.Code)
	}
}

func TestHTMLAuthPage(t *testing.T) {
	g := New("s3cret")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("home"))
	})
	h := g.Middleware(mux)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", rr.Code)
	}
	b, _ := io.ReadAll(rr.Body)
	if !strings.Contains(string(b), "Token required") {
		t.Fatalf("body=%s", b)
	}
}

func TestExpiredTicket(t *testing.T) {
	g := New("s3cret")
	id, _, err := g.MintTicket()
	if err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	g.tickets[id] = time.Now().Add(-time.Second)
	g.mu.Unlock()
	if g.ConsumeTicket(id) {
		t.Fatal("expired ticket accepted")
	}
}
