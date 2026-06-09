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

// TestGUIFontsAreSelfHosted proves the GUI renders with no network: the page
// references no Google host, declares @font-face rules, and every woff2 the CSS
// names is actually served from the embedded tree with a font MIME and valid bytes.
func TestGUIFontsAreSelfHosted(t *testing.T) {
	h := newApp(t).handler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("GET", "/", ""))
	page := w.Body.String()
	for _, bad := range []string{"fonts.googleapis.com", "fonts.gstatic.com"} {
		if strings.Contains(page, bad) {
			t.Fatalf("page still references %s — not offline", bad)
		}
	}
	if !strings.Contains(page, "@font-face") || !strings.Contains(page, "/fonts/") {
		t.Fatal("page is missing @font-face rules pointing at /fonts/")
	}

	faces := []string{
		"cinzel-500.woff2", "cinzel-600.woff2",
		"eb-garamond-400.woff2", "eb-garamond-500.woff2", "eb-garamond-400italic.woff2",
		"cormorant-garamond-400italic.woff2",
	}
	for _, f := range faces {
		// the CSS must name it, and the server must serve it
		if !strings.Contains(page, "/fonts/"+f) {
			t.Errorf("CSS does not reference /fonts/%s", f)
		}
		fw := httptest.NewRecorder()
		h.ServeHTTP(fw, req("GET", "/fonts/"+f, ""))
		if fw.Code != 200 {
			t.Errorf("GET /fonts/%s => %d, want 200", f, fw.Code)
			continue
		}
		if ct := fw.Header().Get("Content-Type"); !strings.Contains(ct, "font/woff2") {
			t.Errorf("/fonts/%s Content-Type = %q, want font/woff2", f, ct)
		}
		if b := fw.Body.Bytes(); len(b) < 4 || string(b[:4]) != "wOF2" {
			t.Errorf("/fonts/%s is not valid woff2 (magic %q)", f, firstN(fw.Body.Bytes(), 4))
		}
	}
}

// TestGUIFontsNoDirListing proves /fonts/ (trailing slash) does not return an index of
// the embedded assets — only named files are served.
func TestGUIFontsNoDirListing(t *testing.T) {
	h := newApp(t).handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("GET", "/fonts/", ""))
	if w.Code != 404 {
		t.Fatalf("GET /fonts/ => %d, want 404 (no directory index)", w.Code)
	}
	// a named font still serves
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req("GET", "/fonts/cinzel-600.woff2", ""))
	if w.Code != 200 {
		t.Fatalf("GET /fonts/cinzel-600.woff2 => %d, want 200", w.Code)
	}
}

func firstN(b []byte, n int) string {
	if len(b) < n {
		n = len(b)
	}
	return string(b[:n])
}
