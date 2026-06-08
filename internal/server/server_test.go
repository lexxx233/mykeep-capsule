package server_test

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mykeep.ai/internal/config"
	"mykeep.ai/internal/domain"
	"mykeep.ai/internal/embed"
	"mykeep.ai/internal/ingest"
	"mykeep.ai/internal/retrieval"
	"mykeep.ai/internal/secret"
	"mykeep.ai/internal/server"
	"mykeep.ai/internal/store"
)

// testStack wires up a real (encrypted, in-RAM) stack exactly as main would,
// then exposes an httptest server in front of the package's http.Handler.
type testStack struct {
	srv   *httptest.Server
	store *store.Store
	embed *embed.HashEmbedder
}

func newStack(t *testing.T, token string) *testStack {
	t.Helper()
	dir := t.TempDir()

	// Random 32-byte DEK -> KeyStore, matching production AES-256-GCM keying.
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatalf("rand: %v", err)
	}
	st, err := store.OpenEncrypted(filepath.Join(dir, "db.enc"), secret.NewKeyStore(dek), store.Options{})
	if err != nil {
		t.Fatalf("OpenEncrypted: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	em := embed.NewHashEmbedder(64)
	in := ingest.New(st, em, 0) // softCap 0 disables capacity warnings
	rc := retrieval.New(st, em)

	cfg := config.Default()
	cfg.Server.Addr = "127.0.0.1:0"
	cfg.Embedding.Model = "hash"
	cfg.Embedding.Dim = 64

	s := server.New(&cfg, st, in, rc, "test-version", "hash", true, token)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return &testStack{srv: ts, store: st, embed: em}
}

// do performs an HTTP request against the test server and decodes the JSON body
// (if dst != nil). It returns the status code.
func (ts *testStack) do(t *testing.T, method, path string, body any, dst any) int {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rdr = strings.NewReader(b)
		default:
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			rdr = bytes.NewReader(raw)
		}
	}
	req, err := http.NewRequest(method, ts.srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			t.Fatalf("decode %s %s body: %v", method, path, err)
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	return resp.StatusCode
}

func TestHealth(t *testing.T) {
	ts := newStack(t, "")

	var h domain.HealthResponse
	code := ts.do(t, http.MethodGet, "/v1/health", nil, &h)
	if code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", code)
	}
	if h.Status != "ok" {
		t.Errorf("status = %q, want ok", h.Status)
	}
	if !h.ContentEncrypted {
		t.Errorf("content_encrypted = false, want true")
	}
	if h.Version != "test-version" {
		t.Errorf("version = %q, want test-version", h.Version)
	}
	if h.Embedder != "hash" {
		t.Errorf("embedder = %q, want hash", h.Embedder)
	}
	if !h.Portable {
		t.Errorf("portable = false, want true")
	}
	if h.MemoryCount != 0 {
		t.Errorf("memory_count = %d, want 0 on fresh store", h.MemoryCount)
	}
}

func TestSettings(t *testing.T) {
	ts := newStack(t, "")

	var s domain.Settings
	code := ts.do(t, http.MethodGet, "/v1/settings", nil, &s)
	if code != http.StatusOK {
		t.Fatalf("settings status = %d, want 200", code)
	}
	if s.EmbeddingModel != "hash" || s.EmbeddingDim != 64 || s.Embedder != "hash" {
		t.Errorf("settings = %+v, want model=hash dim=64 embedder=hash", s)
	}
}

func TestRetainThenRecallRoundTrip(t *testing.T) {
	ts := newStack(t, "")

	const fact = "Emily is the user's roommate since 2026-05-01"
	retainReq := domain.RetainRequest{
		Items: []domain.MemoryItem{
			{Content: fact, Tags: []string{"people"}},
			{Content: "The stove gets hot when it is turned on"},
		},
	}

	var rr domain.RetainResponse
	if code := ts.do(t, http.MethodPost, "/v1/banks/default/retain", retainReq, &rr); code != http.StatusOK {
		t.Fatalf("retain status = %d, want 200", code)
	}
	if !rr.Success {
		t.Errorf("retain success = false, want true")
	}
	if rr.BankID != "default" {
		t.Errorf("retain bank_id = %q, want default", rr.BankID)
	}
	if rr.ItemsCount < 2 {
		t.Errorf("retain items_count = %d, want >= 2", rr.ItemsCount)
	}

	// Recall should return the roommate memory for a related query.
	var recall domain.RecallResponse
	if code := ts.do(t, http.MethodPost, "/v1/banks/default/recall",
		domain.RecallRequest{Query: "who is my roommate"}, &recall); code != http.StatusOK {
		t.Fatalf("recall status = %d, want 200", code)
	}
	if len(recall.Results) == 0 {
		t.Fatalf("recall returned no results")
	}
	var found bool
	for _, r := range recall.Results {
		if r.Text == fact {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("recall results %+v did not contain the retained roommate fact", recall.Results)
	}
}

func TestRecallValidation(t *testing.T) {
	ts := newStack(t, "")

	tests := []struct {
		name string
		body any
		want int
	}{
		{"empty query", domain.RecallRequest{Query: ""}, http.StatusBadRequest},
		{"whitespace query", domain.RecallRequest{Query: "   "}, http.StatusBadRequest},
		{"invalid json", "{not json", http.StatusBadRequest},
		{"valid query", domain.RecallRequest{Query: "anything"}, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code := ts.do(t, http.MethodPost, "/v1/banks/default/recall", tc.body, nil)
			if code != tc.want {
				t.Fatalf("status = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestRetainValidation(t *testing.T) {
	ts := newStack(t, "")

	tests := []struct {
		name string
		path string
		body any
		want int
	}{
		{"bad bank id", "/v1/banks/bad..%2Fx/retain", domain.RetainRequest{Items: []domain.MemoryItem{{Content: "x"}}}, http.StatusBadRequest},
		{"empty items", "/v1/banks/default/retain", domain.RetainRequest{}, http.StatusBadRequest},
		{"empty content", "/v1/banks/default/retain", domain.RetainRequest{Items: []domain.MemoryItem{{Content: ""}}}, http.StatusBadRequest},
		{"invalid json", "/v1/banks/default/retain", "{nope", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code := ts.do(t, http.MethodPost, tc.path, tc.body, nil)
			if code != tc.want {
				t.Fatalf("status = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestReflect(t *testing.T) {
	ts := newStack(t, "")
	ts.do(t, http.MethodPost, "/v1/banks/default/retain", map[string]any{
		"items": []any{
			map[string]any{"content": "Alice works at Acme as an engineer"},
			map[string]any{"content": "Alice enjoys rock climbing on weekends"},
		},
	}, nil)

	var out struct {
		Results []struct {
			Text string `json:"text"`
		} `json:"results"`
	}
	code := ts.do(t, http.MethodPost, "/v1/banks/default/reflect",
		map[string]any{"query": "tell me about Alice"}, &out)
	if code != 200 {
		t.Fatalf("reflect status = %d, want 200", code)
	}
	if len(out.Results) == 0 {
		t.Fatal("reflect returned no results")
	}

	// a reflect with no query is a 400
	if code := ts.do(t, http.MethodPost, "/v1/banks/default/reflect", map[string]any{}, nil); code != 400 {
		t.Fatalf("reflect without query = %d, want 400", code)
	}
}

func TestCaptureEndpoint(t *testing.T) {
	ts := newStack(t, "")
	type cap struct {
		Stored  bool   `json:"stored"`
		Skipped string `json:"skipped"`
	}

	var c cap
	if code := ts.do(t, http.MethodPost, "/v1/banks/default/capture",
		map[string]any{"text": "the user lives in Berlin and codes in Go", "role": "user"}, &c); code != 200 || !c.Stored {
		t.Fatalf("capture = %d %+v, want 200 stored", code, c)
	}

	var dup cap
	ts.do(t, http.MethodPost, "/v1/banks/default/capture",
		map[string]any{"text": "the user lives in Berlin and codes in Go", "role": "user"}, &dup)
	if dup.Stored || dup.Skipped != "duplicate" {
		t.Fatalf("duplicate capture = %+v, want skipped=duplicate", dup)
	}

	if code := ts.do(t, http.MethodPost, "/v1/banks/default/capture", map[string]any{"text": ""}, nil); code != 400 {
		t.Fatalf("empty capture text = %d, want 400", code)
	}

	// memories?tag=capture lists the raw row...
	var ml struct {
		Total int `json:"total"`
	}
	ts.do(t, http.MethodGet, "/v1/banks/default/memories?tag=capture", nil, &ml)
	if ml.Total != 1 {
		t.Fatalf("memories?tag=capture total=%d, want 1", ml.Total)
	}
	// ...but recall excludes it by default (no curated memory exists, so no results).
	var rec struct {
		Results []struct {
			Text string `json:"text"`
		} `json:"results"`
	}
	ts.do(t, http.MethodPost, "/v1/banks/default/recall", map[string]any{"query": "Berlin Go"}, &rec)
	for _, r := range rec.Results {
		if strings.Contains(r.Text, "Berlin") {
			t.Fatalf("recall leaked a capture row: %v", rec.Results)
		}
	}
}

func TestGuideEndpoint(t *testing.T) {
	ts := newStack(t, "")
	resp, err := http.Get(ts.srv.URL + "/v1/guide")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("guide status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"REMEMBER", "REFLECT", "supersedes", "mental_model"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("guide missing %q", want)
		}
	}
}

func TestPutBankValidation(t *testing.T) {
	ts := newStack(t, "")

	tests := []struct {
		name string
		path string
		want int
	}{
		// "../x" url-encoded into a single path segment must be rejected by bankIDRe.
		{"path traversal", "/v1/banks/..%2Fx", http.StatusBadRequest},
		{"leading dot", "/v1/banks/.hidden", http.StatusBadRequest},
		{"valid bank", "/v1/banks/project1", http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name := "label"
			code := ts.do(t, http.MethodPut, tc.path, map[string]any{"name": name}, nil)
			if code != tc.want {
				t.Fatalf("status = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestBanksLifecycle(t *testing.T) {
	ts := newStack(t, "")

	// Create a bank.
	name := "My Project"
	var b domain.Bank
	if code := ts.do(t, http.MethodPut, "/v1/banks/proj", map[string]any{"name": name}, &b); code != http.StatusOK {
		t.Fatalf("put bank status = %d, want 200", code)
	}
	if b.BankID != "proj" {
		t.Errorf("bank_id = %q, want proj", b.BankID)
	}
	if b.Name == nil || *b.Name != name {
		t.Errorf("bank name = %v, want %q", b.Name, name)
	}

	// List should include it.
	var listResp struct {
		Banks []domain.BankSummary `json:"banks"`
	}
	if code := ts.do(t, http.MethodGet, "/v1/banks", nil, &listResp); code != http.StatusOK {
		t.Fatalf("list banks status = %d, want 200", code)
	}
	var seen bool
	for _, bs := range listResp.Banks {
		if bs.BankID == "proj" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("list banks %+v did not include proj", listResp.Banks)
	}

	// Delete it.
	if code := ts.do(t, http.MethodDelete, "/v1/banks/proj", nil, nil); code != http.StatusOK {
		t.Fatalf("delete bank status = %d, want 200", code)
	}

	// Deleting again -> 404.
	if code := ts.do(t, http.MethodDelete, "/v1/banks/proj", nil, nil); code != http.StatusNotFound {
		t.Fatalf("re-delete bank status = %d, want 404", code)
	}
}

func TestListMemoriesPagination(t *testing.T) {
	ts := newStack(t, "")

	// Seed several distinct memories.
	items := make([]domain.MemoryItem, 0, 5)
	for _, c := range []string{"alpha fact", "beta fact", "gamma fact", "delta fact", "epsilon fact"} {
		items = append(items, domain.MemoryItem{Content: c})
	}
	if code := ts.do(t, http.MethodPost, "/v1/banks/default/retain",
		domain.RetainRequest{Items: items}, nil); code != http.StatusOK {
		t.Fatalf("seed retain status = %d, want 200", code)
	}

	// Page 1: limit 2, offset 0.
	var page1 domain.ListMemoriesResponse
	if code := ts.do(t, http.MethodGet, "/v1/banks/default/memories?limit=2&offset=0", nil, &page1); code != http.StatusOK {
		t.Fatalf("list memories status = %d, want 200", code)
	}
	if page1.Limit != 2 || page1.Offset != 0 {
		t.Errorf("page1 limit/offset = %d/%d, want 2/0", page1.Limit, page1.Offset)
	}
	if page1.Total < 5 {
		t.Errorf("page1 total = %d, want >= 5", page1.Total)
	}
	if len(page1.Items) != 2 {
		t.Errorf("page1 returned %d items, want 2", len(page1.Items))
	}

	// Page 2: limit 2, offset 2 -> different items.
	var page2 domain.ListMemoriesResponse
	if code := ts.do(t, http.MethodGet, "/v1/banks/default/memories?limit=2&offset=2", nil, &page2); code != http.StatusOK {
		t.Fatalf("list memories page2 status = %d, want 200", code)
	}
	if page2.Offset != 2 {
		t.Errorf("page2 offset = %d, want 2", page2.Offset)
	}
	if len(page2.Items) > 0 && len(page1.Items) > 0 && page2.Items[0].ID == page1.Items[0].ID {
		t.Errorf("page2 first item id equals page1 first item id (%s); pagination not advancing", page2.Items[0].ID)
	}

	// Default limit/offset when query params are absent.
	var def domain.ListMemoriesResponse
	if code := ts.do(t, http.MethodGet, "/v1/banks/default/memories", nil, &def); code != http.StatusOK {
		t.Fatalf("list memories default status = %d, want 200", code)
	}
	if def.Limit != 100 || def.Offset != 0 {
		t.Errorf("default limit/offset = %d/%d, want 100/0", def.Limit, def.Offset)
	}
}

func TestDeleteMemory(t *testing.T) {
	ts := newStack(t, "")

	if code := ts.do(t, http.MethodPost, "/v1/banks/default/retain",
		domain.RetainRequest{Items: []domain.MemoryItem{{Content: "deletable fact"}}}, nil); code != http.StatusOK {
		t.Fatalf("retain status = %d, want 200", code)
	}

	var list domain.ListMemoriesResponse
	if code := ts.do(t, http.MethodGet, "/v1/banks/default/memories", nil, &list); code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", code)
	}
	if len(list.Items) == 0 {
		t.Fatalf("no memories to delete")
	}
	id := list.Items[0].ID

	// First delete succeeds.
	if code := ts.do(t, http.MethodDelete, "/v1/banks/default/memories/"+id, nil, nil); code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", code)
	}
	// Second delete -> 404.
	if code := ts.do(t, http.MethodDelete, "/v1/banks/default/memories/"+id, nil, nil); code != http.StatusNotFound {
		t.Fatalf("re-delete status = %d, want 404", code)
	}
	// Non-numeric id -> 400.
	if code := ts.do(t, http.MethodDelete, "/v1/banks/default/memories/not-a-number", nil, nil); code != http.StatusBadRequest {
		t.Fatalf("bad id status = %d, want 400", code)
	}
}

func TestBearerTokenGuard(t *testing.T) {
	const token = "s3cr3t-token-value"
	ts := newStack(t, token)

	// Health is exempt from auth.
	if code := ts.do(t, http.MethodGet, "/v1/health", nil, nil); code != http.StatusOK {
		t.Fatalf("health (no token) status = %d, want 200", code)
	}

	// A protected route without the token -> 401.
	req, _ := http.NewRequest(http.MethodGet, ts.srv.URL+"/v1/banks", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-token /v1/banks status = %d, want 401", resp.StatusCode)
	}

	// With the correct token -> 200.
	req2, _ := http.NewRequest(http.MethodGet, ts.srv.URL+"/v1/banks", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("good-token /v1/banks status = %d, want 200", resp2.StatusCode)
	}

	// With a wrong token -> 401.
	req3, _ := http.NewRequest(http.MethodGet, ts.srv.URL+"/v1/banks", nil)
	req3.Header.Set("Authorization", "Bearer wrong-token-entirely")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-token /v1/banks status = %d, want 401", resp3.StatusCode)
	}
}

func TestSnippetText(t *testing.T) {
	withToken := server.SnippetText("127.0.0.1:8765", "abc123")
	if !strings.Contains(withToken, "http://127.0.0.1:8765") {
		t.Errorf("snippet missing base url: %q", withToken)
	}
	if !strings.Contains(withToken, "/v1/guide") {
		t.Errorf("snippet should point at /v1/guide: %q", withToken)
	}
	if !strings.Contains(withToken, "Authorization: Bearer abc123") {
		t.Errorf("snippet with token missing auth line: %q", withToken)
	}
	noToken := server.SnippetText("127.0.0.1:8765", "")
	if strings.Contains(noToken, "Authorization") {
		t.Errorf("snippet without token should omit auth line: %q", noToken)
	}
}
