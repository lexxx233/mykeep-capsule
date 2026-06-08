// Package server is mykeep's local REST API (PLAN §7, §0.0). It is the only
// interface: an AI agent calls it with its shell/fetch tool. No MCP, no setup/unlock
// routes (the server only runs already-unlocked).
package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"mykeep.ai/internal/config"
	"mykeep.ai/internal/domain"
	"mykeep.ai/internal/ingest"
	"mykeep.ai/internal/retrieval"
	"mykeep.ai/internal/store"
)

var bankIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Server struct {
	cfg      *config.Config
	store    *store.Store
	ingest   *ingest.Ingestor
	recall   *retrieval.Recaller
	version  string
	portable bool
	embedder string
	token    string // optional bearer token (require_token)
}

func New(cfg *config.Config, st *store.Store, in *ingest.Ingestor, rc *retrieval.Recaller, version, embedder string, portable bool, token string) *Server {
	return &Server{cfg: cfg, store: st, ingest: in, recall: rc, version: version, embedder: embedder, portable: portable, token: token}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.health)
	mux.HandleFunc("GET /v1/guide", s.guide)
	mux.HandleFunc("GET /v1/settings", s.getSettings)
	mux.HandleFunc("GET /v1/banks", s.listBanks)
	mux.HandleFunc("PUT /v1/banks/{bank}", s.putBank)
	mux.HandleFunc("DELETE /v1/banks/{bank}", s.deleteBank)
	mux.HandleFunc("POST /v1/banks/{bank}/retain", s.retain)
	mux.HandleFunc("POST /v1/banks/{bank}/capture", s.capture)
	mux.HandleFunc("POST /v1/banks/{bank}/recall", s.recallHandler)
	mux.HandleFunc("GET /v1/banks/{bank}/memories", s.listMemories)
	mux.HandleFunc("DELETE /v1/banks/{bank}/memories/{id}", s.deleteMemory)
	mux.HandleFunc("POST /v1/banks/{bank}/reflect", s.reflect)
	return s.guard(mux)
}

// guard enforces loopback (socket + Host header) and the optional bearer token.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.Server.AllowNonLoopback {
			if !isLoopbackAddr(r.RemoteAddr) || !isLoopbackHost(r.Host) {
				writeErr(w, http.StatusForbidden, "forbidden", "loopback only")
				return
			}
		}
		if s.token != "" {
			// /v1/health and /v1/guide are non-sensitive — the agent can fetch its
			// instructions before it has the token.
			if r.URL.Path != "/v1/health" && r.URL.Path != "/v1/guide" && !tokenOK(r, s.token) {
				writeErr(w, http.StatusUnauthorized, "unauthorized", "missing or bad bearer token")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	n, _ := s.store.MemoryCount()
	resp := domain.HealthResponse{
		Status: "ok", Version: s.version, Portable: s.portable,
		ContentEncrypted: true, Embedder: s.embedder,
		MemoryCount: n, DBSizeBytes: s.store.DBSizeBytes(),
	}
	if err := s.store.LastFlushErr(); err != nil {
		resp.Status = "degraded"
		resp.FlushError = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) guide(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(GuideText(s.cfg.Server.Addr)))
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, domain.Settings{
		EmbeddingModel: s.cfg.Embedding.Model,
		EmbeddingDim:   s.cfg.Embedding.Dim,
		Embedder:       s.embedder,
	})
}

func (s *Server) listBanks(w http.ResponseWriter, _ *http.Request) {
	banks, err := s.store.ListBanks()
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"banks": banks})
}

func (s *Server) putBank(w http.ResponseWriter, r *http.Request) {
	bank := r.PathValue("bank")
	if !bankIDRe.MatchString(bank) {
		writeErr(w, 400, "bad_request", "invalid bank_id")
		return
	}
	var body struct {
		Name *string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	b, err := s.store.PutBank(bank, body.Name)
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeJSON(w, 200, b)
}

func (s *Server) deleteBank(w http.ResponseWriter, r *http.Request) {
	bank := r.PathValue("bank")
	ok, err := s.store.DeleteBank(bank)
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	if !ok {
		writeErr(w, 404, "not_found", "bank not found")
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true, "bank_id": bank})
}

func (s *Server) retain(w http.ResponseWriter, r *http.Request) {
	bank := r.PathValue("bank")
	if !bankIDRe.MatchString(bank) {
		writeErr(w, 400, "bad_request", "invalid bank_id")
		return
	}
	var req domain.RetainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	resp, err := s.ingest.Retain(r.Context(), bank, req)
	if err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	writeJSON(w, 200, resp)
}

// capture logs one raw turn as a low-tier, mechanically-deduped safety-net memory
// (auto-retain). A deduped/trivial skip is a 200 with stored:false, not an error.
func (s *Server) capture(w http.ResponseWriter, r *http.Request) {
	bank := r.PathValue("bank")
	if !bankIDRe.MatchString(bank) {
		writeErr(w, 400, "bad_request", "invalid bank_id")
		return
	}
	var req domain.CaptureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeErr(w, 400, "bad_request", "text is required")
		return
	}
	resp, err := s.ingest.Capture(r.Context(), bank, req)
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeJSON(w, 200, resp)
}

func (s *Server) recallHandler(w http.ResponseWriter, r *http.Request) {
	bank := r.PathValue("bank")
	var req domain.RecallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeErr(w, 400, "bad_request", "query is required")
		return
	}
	resp, err := s.recall.Recall(r.Context(), bank, req)
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeJSON(w, 200, resp)
}

// reflect returns a broad, synthesis-oriented context bundle for the agent (PLAN §0.0).
func (s *Server) reflect(w http.ResponseWriter, r *http.Request) {
	bank := r.PathValue("bank")
	var req domain.RecallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeErr(w, 400, "bad_request", "query is required")
		return
	}
	resp, err := s.recall.Reflect(r.Context(), bank, req)
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeJSON(w, 200, resp)
}

func (s *Server) listMemories(w http.ResponseWriter, r *http.Request) {
	bank := r.PathValue("bank")
	limit := atoiDefault(r.URL.Query().Get("limit"), 100)
	offset := atoiDefault(r.URL.Query().Get("offset"), 0)
	factType := r.URL.Query().Get("type") // e.g. experience
	tag := r.URL.Query().Get("tag")       // e.g. capture (to read the raw substrate for distillation)
	items, total, err := s.store.ListMemoriesFiltered(bank, limit, offset, factType, tag)
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeJSON(w, 200, domain.ListMemoriesResponse{Items: items, Total: total, Limit: limit, Offset: offset})
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	bank := r.PathValue("bank")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad_request", "invalid id")
		return
	}
	ok, err := s.store.DeleteMemory(bank, id)
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	if !ok {
		writeErr(w, 404, "not_found", "memory not found")
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]any{"error": kind, "message": msg})
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func tokenOK(r *http.Request, want string) bool {
	h := r.Header.Get("Authorization")
	got := strings.TrimPrefix(h, "Bearer ")
	return len(got) == len(want) && subtleEqual(got, want)
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func isLoopbackAddr(remoteAddr string) bool {
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

// GuideText is the full operating manual for the host LLM. Because mykeep runs no
// model of its own, the calling agent must know the retain/recall/reflect/supersede
// protocol — this is mykeep's equivalent of hindsight's reflect-agent system prompt.
func GuideText(addr string) string {
	base := "http://" + addr
	return strings.ReplaceAll(`mykeep — your persistent memory. You do the thinking; mykeep stores, searches, and forgets.
Base URL: {BASE}   (local loopback, JSON). Default bank: "default".

CORE LOOP — how you get smarter over time:
  1. RECALL or REFLECT before answering anything about the user or project.
  2. Reason with your own model.
  3. RETAIN new facts and your conclusions; SUPERSEDE outdated conclusions.

REMEMBER — POST {BASE}/v1/banks/default/retain
  {"items":[{
     "content":   "<concise, self-contained memory>",
     "type":      "experience",           // experience(default) | world | observation | mental_model
     "tags":      ["user_a"],             // scope (per user/project); recall/reflect can filter
     "entities":  [{"text":"Alice"}],     // link memories that share a person/thing
     "timestamp": "2026-05-01T10:00:00Z", // when it happened (ISO8601); omit = now
     "supersedes":["42"]                  // ids this replaces — mykeep deletes them
  }]}
  - Store the moment you learn something durable (names, preferences, decisions, state).
  - Keep each memory atomic. Don't store transient chit-chat.

MEMORY TYPES (the knowledge hierarchy — reflect returns the higher tiers first):
  - experience / world : raw facts and events (most retains).
  - observation        : a pattern you noticed across facts.
  - mental_model       : your curated synthesis about a topic/person (highest tier).

RECALL — POST {BASE}/v1/banks/default/recall  {"query":"...", "tags":[...], "max_tokens":4096}
  - The tight, most-relevant set to answer a specific question.

REFLECT — POST {BASE}/v1/banks/default/reflect  {"query":"<topic/person>"}
  - A broad bundle for synthesis: your saved mental_models/observations first, plus
    memories sharing entities. Use for "what do I know about X?" or to form/update a model.
  - After reflecting, RETAIN your conclusion as type "mental_model" with
    "supersedes":["<old model id>"] so the stale synthesis is removed.

CAPTURE & DISTILL (auto-retain safety net):
  - A host hook may auto-log each raw turn via POST {BASE}/v1/banks/default/capture
    {text, role} — stored as a low-tier "experience" tagged "capture", deduped. You rarely
    call this yourself, and these raw rows are HIDDEN from recall/reflect by default.
  - Periodically (or when nudged), DISTILL: read recent raw captures with
    GET {BASE}/v1/banks/default/memories?type=experience&tag=capture&limit=50 , then promote the
    durable ones via retain {type:"mental_model"|"observation", supersedes:[<raw capture ids>]} —
    which folds them up the hierarchy and deletes the raw rows. Leave or supersede the noise.
  - To search the raw log directly, pass {"include_captures": true} to recall.

FORGETTING:
  - mykeep deletes whatever you supersede and prunes orphaned data automatically.
  - It does NOT auto-summarize or dedup for you — that is your job; you are the reasoner.

NOTES:
  - Everything is encrypted at rest on the user's USB stick. No cloud, no recovery.
  - Use one bank per project/user if you want isolated memory sets.`, "{BASE}", base)
}

// Snippet returns the copy-paste integration block for this server (PLAN §0.0, D20).
func (s *Server) Snippet() string { return SnippetText(s.cfg.Server.Addr, s.token) }

// SnippetText builds the paste-ready integration block for a given address/token.
func SnippetText(addr, token string) string {
	base := "http://" + addr
	var auth string
	if token != "" {
		auth = fmt.Sprintf("\nAuth: add header  Authorization: Bearer %s", token)
	}
	return strings.Join([]string{
		"─────────────────────────────────────────────────────────────────",
		"You have a persistent local memory (mykeep) at " + base + ".",
		"▶ First, fetch your instructions:  GET " + base + "/v1/guide",
		"Then follow them — remember facts about the user/project as you learn them, and",
		"recall before you answer. Use your shell or fetch tool to call the API." + auth,
		"(Tip: for hands-off retention, install the capture hook — see integrations/claude-code.)",
		"─────────────────────────────────────────────────────────────────",
	}, "\n")
}
