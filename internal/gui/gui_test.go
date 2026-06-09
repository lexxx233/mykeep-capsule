package gui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mykeep.ai/internal/paths"
)

// req builds a loopback request (RemoteAddr + Host) unless overridden.
func req(method, path string, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.RemoteAddr = "127.0.0.1:54321"
	r.Host = "127.0.0.1:8765"
	return r
}

func newApp(t *testing.T) *App {
	t.Helper()
	return New(paths.Layout{DataDir: t.TempDir(), Portable: true}, "test", "127.0.0.1:8765")
}

func TestGUIServesPageAndState(t *testing.T) {
	h := newApp(t).handler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("GET", "/", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "mykeep") {
		t.Fatalf("GET / => %d, body has mykeep=%v", w.Code, strings.Contains(w.Body.String(), "mykeep"))
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, req("GET", "/api/state", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"first_launch":true`) ||
		!strings.Contains(w.Body.String(), `"unlocked":false`) {
		t.Fatalf("state => %d %s", w.Code, w.Body.String())
	}
}

func TestGUIv1LockedReturns423(t *testing.T) {
	h := newApp(t).handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("GET", "/v1/health", ""))
	if w.Code != http.StatusLocked {
		t.Fatalf("locked /v1/health => %d, want 423", w.Code)
	}
}

func TestGUILoopbackGuard(t *testing.T) {
	h := newApp(t).handler()

	// spoofed (non-loopback) Host is rejected
	r := req("GET", "/api/state", "")
	r.Host = "evil.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("evil Host => %d, want 403", w.Code)
	}

	// non-loopback socket is rejected
	r = req("GET", "/api/state", "")
	r.RemoteAddr = "203.0.113.7:9999"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("remote socket => %d, want 403", w.Code)
	}
}

func TestGUISetupRequiresAPassword(t *testing.T) {
	// No complexity policy, but an empty password is still rejected at the API.
	h := newApp(t).handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("POST", "/api/setup", `{"password":""}`))
	if w.Code != 400 {
		t.Fatalf("empty setup => %d, want 400", w.Code)
	}
}

// TestGUIRendersOffline proves the GUI needs no network: the page pulls in no web
// fonts at all (system fonts only) — no Google host, no @font-face, no /fonts/ asset.
func TestGUIRendersOffline(t *testing.T) {
	h := newApp(t).handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("GET", "/", ""))
	page := w.Body.String()
	for _, bad := range []string{"fonts.googleapis.com", "fonts.gstatic.com", "@font-face", "/fonts/"} {
		if strings.Contains(page, bad) {
			t.Fatalf("page references %q — should use system fonts only", bad)
		}
	}
}
