package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thanhenti/bepilot/internal/agent"
	"github.com/thanhenti/bepilot/internal/api"
	"github.com/thanhenti/bepilot/internal/config"
	"github.com/thanhenti/bepilot/internal/llm"
	"github.com/thanhenti/bepilot/internal/llm/fakeprovider"
	"github.com/thanhenti/bepilot/internal/logging"
	"github.com/thanhenti/bepilot/internal/retrieval"
	"github.com/thanhenti/bepilot/internal/server"
	"github.com/thanhenti/bepilot/internal/skills"
	"github.com/thanhenti/bepilot/internal/store"
	"github.com/thanhenti/bepilot/internal/tools"
)

// These tests need a Postgres with pgvector. Set:
//
//	TEST_DATABASE_URL=postgres://bepilot:bepilot@localhost:5433/bepilot?sslmode=disable
func testDSN(t *testing.T) string {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run integration tests")
	}
	return dsn
}

type testEnv struct {
	addr   string
	apiKey string
	store  *store.Store
	cancel context.CancelFunc
}

func setup(t *testing.T) *testEnv {
	t.Helper()
	dsn := testDSN(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	log := logging.New(config.Log{Level: "warn", Format: "text"})

	st, err := store.Open(ctx, config.DB{DSN: dsn, MaxConns: 5, MinConns: 1, AutoMigrate: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(st.Close)
	truncate(t, st)

	embedder := retrieval.NewHashEmbedder(1536)
	skillSvc := skills.NewService(st.Skills, embedder, "../../skills", log)
	if _, err := skillSvc.Sync(ctx); err != nil {
		t.Fatalf("skills sync: %v", err)
	}

	reg, err := llm.NewRegistry(config.LLM{
		DefaultProvider: "fake", DefaultModel: "bepilot-fake-1", MaxTokens: 1024,
		Providers: map[string]config.ProviderCfg{"fake": {Kind: "fake"}},
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	reg.Register("fake", fakeprovider.New("pdf-forms"))
	if err := reg.Validate(); err != nil {
		t.Fatal(err)
	}

	toolReg, err := tools.NewRegistry(skillSvc, nil, st.TaskResults)
	if err != nil {
		t.Fatalf("tools: %v", err)
	}

	acfg := config.AgentCfg{
		Identity: "You are bepilot.", ResponseStyle: "Be concise.",
		MaxSteps: 8, HistoryTokenBudget: 24000, SummarizeEveryN: 0, SkillTopK: 4,
		PingInterval: time.Second,
	}
	lcfg := config.LLM{DefaultProvider: "fake", DefaultModel: "bepilot-fake-1", MaxTokens: 1024}
	ag := agent.New(st, reg, toolReg, skillSvc, acfg, lcfg, log)

	h := &api.Handlers{Store: st, Agent: ag, Registry: reg, Skills: skillSvc, LLM: lcfg, Agentcfg: acfg, Log: log}

	// pick a free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	hz := server.New(config.HTTP{Addr: addr, ReadTimeout: 30 * time.Second}, h, log)
	go hz.Spin()
	t.Cleanup(func() { _ = hz.Shutdown(context.Background()) })
	waitReady(t, addr)

	// seed a user + key
	user, err := st.Users.Create(ctx, "it@bepilot.local", "IT")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, _, err := st.APIKeys.Issue(ctx, user.ID, "it")
	if err != nil {
		t.Fatal(err)
	}

	return &testEnv{addr: addr, apiKey: plaintext, store: st, cancel: cancel}
}

func truncate(t *testing.T, st *store.Store) {
	_, err := st.Pool.Exec(context.Background(),
		`TRUNCATE users, api_keys, sessions, messages, content_blocks, agent_runs, task_results RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func waitReady(t *testing.T, addr string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("server did not become ready")
}

func (e *testEnv) do(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://"+e.addr+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", e.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestMessagesBufferedCreatesSessionAndUsesSkill(t *testing.T) {
	e := setup(t)

	resp := e.do(t, http.MethodPost, "/v1/messages", map[string]any{
		"model":      "bepilot-fake-1",
		"max_tokens": 512,
		"messages":   []map[string]any{{"role": "user", "content": "help me fill in a pdf form with my address"}},
	})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	var msg struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Session    struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "message" || msg.Role != "assistant" {
		t.Fatalf("bad response envelope: %+v", msg)
	}
	if msg.Session.ID == "" {
		t.Fatal("expected a session to be created")
	}
	var sawToolUse, sawText bool
	for _, c := range msg.Content {
		if c.Type == "tool_use" && c.Name == "load_skill" {
			sawToolUse = true
		}
		if c.Type == "text" && c.Text != "" {
			sawText = true
		}
	}
	if !sawToolUse {
		t.Fatalf("expected the agent to call load_skill without being told to; content=%+v", msg.Content)
	}
	if !sawText {
		t.Fatal("expected a final text block")
	}

	// sessions-by-user
	lresp := e.do(t, http.MethodGet, "/v1/sessions", nil)
	defer lresp.Body.Close()
	var list struct {
		Data []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"data"`
	}
	json.NewDecoder(lresp.Body).Decode(&list)
	if len(list.Data) != 1 || list.Data[0].ID != msg.Session.ID {
		t.Fatalf("expected exactly the new session in the list: %+v", list.Data)
	}

	// transcript
	dresp := e.do(t, http.MethodGet, "/v1/sessions/"+msg.Session.ID, nil)
	defer dresp.Body.Close()
	var detail struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
			} `json:"content"`
		} `json:"messages"`
	}
	json.NewDecoder(dresp.Body).Decode(&detail)
	if len(detail.Messages) != 2 {
		t.Fatalf("want user+assistant messages, got %d", len(detail.Messages))
	}
	types := map[string]int{}
	for _, b := range detail.Messages[1].Content {
		types[b.Type]++
	}
	if types["tool_use"] == 0 || types["tool_result"] == 0 || types["text"] == 0 {
		t.Fatalf("assistant transcript missing block types: %+v", types)
	}
}

func TestDocsEndpoints(t *testing.T) {
	e := setup(t)

	// spec is public (no key) and parseable
	resp, err := http.Get("http://" + e.addr + "/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("/openapi.yaml status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "swagger:") || !strings.Contains(string(body), "/v1/messages") {
		t.Fatalf("/openapi.yaml body unexpected: %.120s", body)
	}

	// /docs redirects to the Swagger UI
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	dresp, err := noRedirect.Get("http://" + e.addr + "/docs")
	if err != nil {
		t.Fatal(err)
	}
	defer dresp.Body.Close()
	if dresp.StatusCode != 302 || !strings.HasPrefix(dresp.Header.Get("Location"), "/swagger") {
		t.Fatalf("/docs should redirect to /swagger, got %d %q", dresp.StatusCode, dresp.Header.Get("Location"))
	}

	// Swagger UI index renders
	uiresp, err := http.Get("http://" + e.addr + "/swagger/index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer uiresp.Body.Close()
	if uiresp.StatusCode != 200 {
		t.Fatalf("/swagger/index.html status %d", uiresp.StatusCode)
	}
}

func TestMessagesStreamingEmitsAnthropicEvents(t *testing.T) {
	e := setup(t)

	b, _ := json.Marshal(map[string]any{
		"model":      "bepilot-fake-1",
		"max_tokens": 512,
		"stream":     true,
		"messages":   []map[string]any{{"role": "user", "content": "clean up this messy csv of customers"}},
	})
	req, _ := http.NewRequest(http.MethodPost, "http://"+e.addr+"/v1/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", e.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}

	var eventTypes []string
	var blockStartIdx []int
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	deadline := time.Now().Add(15 * time.Second)
	var lastEvent string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event:") {
			lastEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			eventTypes = append(eventTypes, lastEvent)
		}
		if strings.HasPrefix(line, "data:") && lastEvent == "content_block_start" {
			var p struct {
				Index int `json:"index"`
			}
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &p)
			blockStartIdx = append(blockStartIdx, p.Index)
		}
		if strings.TrimSpace(line) == "" && len(eventTypes) > 0 && eventTypes[len(eventTypes)-1] == "message_stop" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out reading SSE stream")
		}
	}

	joined := strings.Join(eventTypes, ",")
	for _, want := range []string{
		"message_start", "content_block_start", "content_block_delta", "content_block_stop",
		"tool_execution_start", "tool_execution_stop", "message_delta", "message_stop",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("stream missing %q; got: %s", want, joined)
		}
	}
	if first, last := eventTypes[0], eventTypes[len(eventTypes)-1]; first != "message_start" || last != "message_stop" {
		t.Fatalf("stream framing: first=%s last=%s", first, last)
	}
	// content-block indices must be contiguous from 0.
	for i, idx := range blockStartIdx {
		if idx != i {
			t.Fatalf("content_block_start indices not contiguous: %v", blockStartIdx)
		}
	}
}

func TestAGUIRunAgentStreamsProtocolEvents(t *testing.T) {
	e := setup(t)

	b, _ := json.Marshal(map[string]any{
		"threadId": "",
		"runId":    "run-it-1",
		"messages": []map[string]any{
			{"id": "m1", "role": "user", "content": "help me fill in a pdf form with my address"},
		},
	})
	req, _ := http.NewRequest(http.MethodPost, "http://"+e.addr+"/v1/ag-ui/run", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", e.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}

	var types []string
	var threadID string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	deadline := time.Now().Add(15 * time.Second)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			if time.Now().After(deadline) {
				t.Fatal("timed out reading AG-UI stream")
			}
			continue
		}
		var p struct {
			Type     string `json:"type"`
			ThreadID string `json:"threadId"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &p); err != nil {
			t.Fatalf("non-JSON AG-UI frame: %q", line)
		}
		types = append(types, p.Type)
		if p.Type == "RUN_STARTED" {
			threadID = p.ThreadID
		}
		if p.Type == "RUN_FINISHED" || p.Type == "RUN_ERROR" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out reading AG-UI stream")
		}
	}

	joined := strings.Join(types, ",")
	for _, want := range []string{
		"RUN_STARTED", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END",
		"TOOL_CALL_START", "TOOL_CALL_ARGS", "TOOL_CALL_END", "TOOL_CALL_RESULT", "RUN_FINISHED",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("AG-UI stream missing %q; got: %s", want, joined)
		}
	}
	if types[0] != "RUN_STARTED" || types[len(types)-1] != "RUN_FINISHED" {
		t.Fatalf("AG-UI framing: first=%s last=%s", types[0], types[len(types)-1])
	}
	if threadID == "" {
		t.Fatal("RUN_STARTED did not carry a threadId for the new session")
	}

	// The new thread id must address a real session for this user.
	sresp := e.do(t, http.MethodGet, "/v1/sessions/"+threadID, nil)
	defer sresp.Body.Close()
	if sresp.StatusCode != 200 {
		t.Fatalf("thread id %q is not a usable session: status %d", threadID, sresp.StatusCode)
	}
}
