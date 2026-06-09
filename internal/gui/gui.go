// Package gui serves mykeep's cross-platform GUI: a local web app (embedded via
// go:embed, opened in the default browser) that collects the password, unlocks the
// encrypted store, and shows a dashboard. Pure Go, no GUI toolkit, no CGo — every OS
// has a browser, which keeps the single static binary intact.
package gui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"mykeep.ai/internal/app"
	"mykeep.ai/internal/paths"
	"mykeep.ai/internal/secret"
	"mykeep.ai/internal/server"
)

// The whole web/ tree is embedded (the dashboard page plus self-hosted woff2
// fonts under web/fonts/), so the GUI renders identically with no network.
//
//go:embed web
var webFS embed.FS

// webRoot is web/ rooted so request paths like /fonts/x.woff2 map straight to
// fonts/x.woff2; indexHTML is the dashboard, read once at startup.
var webRoot = mustSub(webFS, "web")
var indexHTML = mustReadFile(webRoot, "index.html")

func init() {
	// Guarantee a correct Content-Type for the bundled fonts even on hosts whose
	// mime table lacks woff2 (e.g. the stick plugged into a fresh Windows box).
	_ = mime.AddExtensionType(".woff2", "font/woff2")
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	s, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return s
}

func mustReadFile(fsys fs.FS, name string) []byte {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		panic(err)
	}
	return b
}

type App struct {
	layout  paths.Layout
	version string
	addr    string

	mu   sync.Mutex
	rt   *app.Runtime
	apiH http.Handler // server.Handler over rt; nil until unlocked
}

func New(layout paths.Layout, version, addr string) *App {
	return &App{layout: layout, version: version, addr: addr}
}

// Run starts the GUI server on addr, opens the browser, and blocks until a signal.
func (a *App) Run() error {
	srv := &http.Server{Addr: a.addr, Handler: a.handler()}
	errCh := make(chan error, 1)
	go func() {
		if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			errCh <- e
		}
	}()

	url := "http://" + a.addr
	fmt.Printf("\n🪟  mykeep GUI: %s  (opening your browser…)\n", url)
	if err := openBrowser(url); err != nil {
		fmt.Fprintf(os.Stderr, "couldn't open a browser automatically — visit %s\n", url)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
		fmt.Fprintln(os.Stderr, "\nshutting down…")
	case e := <-errCh:
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rt != nil {
		return a.rt.Close() // final flush + zeroize
	}
	return nil
}

func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.index)                   // exactly "/", so it doesn't shadow /v1/ or /api/
	mux.Handle("GET /fonts/", noDirList(http.FileServerFS(webRoot))) // self-hosted woff2 files; no directory index
	mux.HandleFunc("GET /api/state", a.state)
	mux.HandleFunc("POST /api/setup", a.setup)
	mux.HandleFunc("POST /api/unlock", a.unlock)
	mux.HandleFunc("POST /api/lock", a.lock)
	mux.HandleFunc("GET /api/snippet", a.snippet)
	mux.Handle("/v1/", http.HandlerFunc(a.v1)) // gated REST API
	return loopbackGuard(mux)
}

func (a *App) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

func (a *App) state(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	unlocked := a.rt != nil
	a.mu.Unlock()
	writeJSON(w, 200, map[string]any{
		"first_launch": a.layout.IsFirstLaunch(),
		"unlocked":     unlocked,
		"version":      a.version,
		"portable":     a.layout.Portable,
	})
}

type passReq struct {
	Password string `json:"password"`
}

func (a *App) setup(w http.ResponseWriter, r *http.Request) {
	if !a.layout.IsFirstLaunch() {
		writeErr(w, 409, "already set up")
		return
	}
	pw := decodePass(w, r)
	if pw == nil {
		return
	}
	defer wipe(pw)
	a.open(w, pw, true) // no complexity policy — the user owns their passphrase strength
}

func (a *App) unlock(w http.ResponseWriter, r *http.Request) {
	pw := decodePass(w, r)
	if pw == nil {
		return
	}
	defer wipe(pw)
	a.open(w, pw, false)
}

// open builds the runtime (loads the model + decrypts the DB — may take a few
// seconds) and installs the REST handler.
func (a *App) open(w http.ResponseWriter, pw []byte, firstLaunch bool) {
	a.mu.Lock()
	if a.rt != nil {
		a.mu.Unlock()
		writeJSON(w, 200, map[string]any{"unlocked": true})
		return
	}
	a.mu.Unlock()

	rt, err := app.Open(context.Background(), a.layout, pw, firstLaunch, a.version)
	if err != nil {
		if err == secret.ErrWrongPassphrase {
			writeErr(w, 401, "wrong password")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	api := server.New(rt.Config, rt.Store, rt.Ingest, rt.Recall, a.version, rt.EmbedderName(), a.layout.Portable, "").Handler()

	a.mu.Lock()
	a.rt = rt
	a.apiH = api
	a.mu.Unlock()
	writeJSON(w, 200, map[string]any{"unlocked": true})
}

func (a *App) lock(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	rt := a.rt
	a.rt, a.apiH = nil, nil
	a.mu.Unlock()
	if rt != nil {
		_ = rt.Close()
	}
	writeJSON(w, 200, map[string]any{"unlocked": false})
}

func (a *App) snippet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"snippet": server.SnippetText(a.addr, "")})
}

func (a *App) v1(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	h := a.apiH
	a.mu.Unlock()
	if h == nil {
		writeErr(w, 423, "locked — unlock mykeep first")
		return
	}
	h.ServeHTTP(w, r)
}

// --- helpers ---

func decodePass(w http.ResponseWriter, r *http.Request) []byte {
	var req passReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		writeErr(w, 400, "password required")
		return nil
	}
	return []byte(req.Password)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// noDirList serves files but returns 404 for a directory path (trailing slash), so the
// embedded /fonts/ tree can't be enumerated via an auto-generated index.
func noDirList(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loopbackGuard rejects non-loopback sockets and Host headers (PLAN §7.4).
func loopbackGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) || !isLoopbackHost(r.Host) {
			http.Error(w, "loopback only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// openBrowser opens url in the OS default browser (pure Go via os/exec, no CGo).
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
