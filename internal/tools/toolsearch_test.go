package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

type echoArgs struct {
	Text string `json:"text" jsonschema:"required" jsonschema_description:"text to echo"`
}

func fakeExternal(t *testing.T, name, desc string) tool.InvokableTool {
	t.Helper()
	it, err := utils.InferTool(name, desc, func(_ context.Context, a echoArgs) (string, error) {
		return "ran:" + a.Text, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func newReg(t *testing.T, external ...[2]string) *Registry {
	t.Helper()
	r, err := NewRegistry(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range external {
		if _, err := r.Register(context.Background(), fakeExternal(t, e[0], e[1])); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

var githubAndSlack = [][2]string{
	{"mcp__github__create_issue", "Create an issue in a GitHub repository"},
	{"mcp__github__search_code", "Search code across GitHub repositories"},
	{"mcp__slack__send_message", "Send a message to a Slack channel"},
}

func TestNoExternalTools(t *testing.T) {
	r := newReg(t)
	s := r.NewSession()

	if s.Visible(ToolSearchName) {
		t.Error("tool_search should be hidden when there is nothing to search")
	}
	for _, n := range []string{"current_time", "http_fetch", "load_skill", "web_search"} {
		if !s.Visible(n) {
			t.Errorf("built-in %s should be visible", n)
		}
	}
	if got := s.DeferredNames(); len(got) != 0 {
		t.Errorf("DeferredNames = %v, want none", got)
	}
	if got := len(s.Executable()); got != 5 {
		t.Errorf("Executable = %d tools, want the 5 built-ins", got)
	}

	// Even if the model calls tool_search anyway, it degrades gracefully.
	ctx := WithSession(context.Background(), s)
	out := runSearch(t, ctx, r, "select:whatever")
	if len(out.Matches) != 0 || out.TotalDeferred != 0 || len(out.NotFound) != 1 {
		t.Errorf("unexpected result on empty catalog: %+v", out)
	}
	out = runSearch(t, ctx, r, "github")
	if len(out.Matches) != 0 || out.Note == "" {
		t.Errorf("unexpected keyword result on empty catalog: %+v", out)
	}
	// And with no session in ctx at all.
	out = runSearch(t, context.Background(), r, "github")
	if len(out.Matches) != 0 {
		t.Errorf("unexpected result without session: %+v", out)
	}
}

func runSearch(t *testing.T, ctx context.Context, r *Registry, query string) searchResult {
	t.Helper()
	var ts tool.InvokableTool
	for _, e := range r.Builtin() {
		if e.Name == ToolSearchName {
			ts = e.tool
		}
	}
	args, _ := json.Marshal(map[string]any{"query": query})
	raw, err := ts.InvokableRun(ctx, string(args))
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	var res searchResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("bad result %q: %v", raw, err)
	}
	return res
}

func TestExternalToolsAreDeferredUntilSearched(t *testing.T) {
	r := newReg(t, githubAndSlack...)
	s := r.NewSession()
	ctx := WithSession(context.Background(), s)

	if !s.Visible(ToolSearchName) {
		t.Fatal("tool_search should be visible when deferred tools exist")
	}
	if s.Visible("mcp__github__create_issue") {
		t.Fatal("deferred tool visible before discovery")
	}
	if got := len(s.DeferredNames()); got != 3 {
		t.Fatalf("DeferredNames = %d, want 3", got)
	}

	// Calling a deferred tool that was never loaded is refused.
	var gate tool.InvokableTool
	for _, bt := range s.Executable() {
		info, _ := bt.(tool.InvokableTool).Info(ctx)
		if info.Name == "mcp__github__create_issue" {
			gate = bt.(tool.InvokableTool)
		}
	}
	if _, err := gate.InvokableRun(ctx, `{"text":"x"}`); err == nil || !strings.Contains(err.Error(), "tool_search") {
		t.Fatalf("expected a 'load it first' error, got %v", err)
	}

	res := runSearch(t, ctx, r, "+github issue")
	// +github filters to the github tools; "issue" only ranks, so create_issue
	// comes first and slack is excluded.
	if len(res.Matches) != 2 || res.Matches[0].Name != "mcp__github__create_issue" {
		t.Fatalf("matches = %v", res.Matches)
	}
	if !strings.Contains(string(res.Matches[0].InputSchema), `"text"`) {
		t.Errorf("input schema missing: %s", res.Matches[0].InputSchema)
	}
	if !s.Visible("mcp__github__create_issue") || s.Visible("mcp__slack__send_message") {
		t.Error("only the matched tools should be visible")
	}
	if out, err := gate.InvokableRun(ctx, `{"text":"x"}`); err != nil || out != `"ran:x"` && out != "ran:x" {
		t.Errorf("loaded tool should run: out=%q err=%v", out, err)
	}
}

func TestSearchForms(t *testing.T) {
	s := newReg(t, githubAndSlack...).NewSession()

	got, missing := s.Search("select:MCP__SLACK__send_message, nope", 0)
	if len(got) != 1 || got[0].Name != "mcp__slack__send_message" || len(missing) != 1 || missing[0] != "nope" {
		t.Errorf("select: got=%v missing=%v", names(got), missing)
	}

	got, _ = s.Search("github", 0)
	if len(got) != 2 {
		t.Errorf("keyword github: %v", names(got))
	}
	got, _ = s.Search("+slack github", 0)
	if len(got) != 1 || got[0].Name != "mcp__slack__send_message" {
		t.Errorf("required +slack: %v", names(got))
	}
	got, _ = s.Search("github", 1)
	if len(got) != 1 {
		t.Errorf("limit not applied: %v", names(got))
	}
	got, _ = s.Search("zzzz", 0)
	if len(got) != 0 {
		t.Errorf("nonsense query matched: %v", names(got))
	}
	got, _ = s.Search("   ", 0)
	if len(got) != 0 {
		t.Errorf("blank query matched: %v", names(got))
	}
}

func names(es []*Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
	}
	return out
}

func TestRestoreFromHistory(t *testing.T) {
	r := newReg(t, githubAndSlack...)
	first := r.NewSession()
	ctx := WithSession(context.Background(), first)
	runSearch(t, ctx, r, "select:mcp__slack__send_message")

	// Rebuild what history replay would hand the next turn.
	var content string
	{
		args, _ := json.Marshal(map[string]any{"query": "select:mcp__slack__send_message"})
		for _, e := range r.Builtin() {
			if e.Name == ToolSearchName {
				content, _ = e.tool.InvokableRun(WithSession(context.Background(), r.NewSession()), string(args))
			}
		}
	}
	next := r.NewSession()
	next.Restore([]*schema.Message{
		{Role: schema.User, Content: "hi"},
		{Role: schema.Tool, ToolName: ToolSearchName, Content: content},
		{Role: schema.Tool, ToolName: "other", Content: `{"matches":[{"name":"mcp__github__create_issue"}]}`},
	})
	if !next.Visible("mcp__slack__send_message") {
		t.Error("tool from an earlier tool_search should be restored")
	}
	if next.Visible("mcp__github__create_issue") {
		t.Error("only tool_search results may restore tools")
	}

	// A tool that has since been unregistered is ignored, not an error.
	r.Unregister("mcp__slack__send_message")
	gone := r.NewSession()
	gone.Restore([]*schema.Message{{Role: schema.Tool, ToolName: ToolSearchName, Content: content}})
	if gone.Visible("mcp__slack__send_message") {
		t.Error("unregistered tool must not become visible")
	}
}

func TestRegisterRejectsBuiltinCollision(t *testing.T) {
	r := newReg(t)
	if _, err := r.Register(context.Background(), fakeExternal(t, "current_time", "dup")); err == nil {
		t.Error("expected collision error")
	}
}

type recModel struct {
	bound []string
	seen  *[][]string
}

func (m *recModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	*m.seen = append(*m.seen, m.bound)
	return &schema.Message{Role: schema.Assistant}, nil
}
func (m *recModel) Stream(ctx context.Context, in []*schema.Message, o ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, _ := m.Generate(ctx, in, o...)
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}
func (m *recModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := &recModel{seen: m.seen}
	for _, i := range infos {
		cp.bound = append(cp.bound, i.Name)
	}
	return cp, nil
}

func TestWrapModelBindsOnlyVisibleTools(t *testing.T) {
	r := newReg(t, githubAndSlack...)
	s := r.NewSession()

	var infos []*schema.ToolInfo
	for _, bt := range s.Executable() {
		info, _ := bt.Info(context.Background())
		infos = append(infos, info)
	}
	var seen [][]string
	wrapped, err := s.WrapModel(&recModel{seen: &seen}).WithTools(infos)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := wrapped.Generate(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	has := func(list []string, n string) bool {
		for _, x := range list {
			if x == n {
				return true
			}
		}
		return false
	}
	if has(seen[0], "mcp__slack__send_message") || !has(seen[0], ToolSearchName) || !has(seen[0], "load_skill") {
		t.Errorf("first call bound %v", seen[0])
	}

	s.Discover("mcp__slack__send_message")
	if _, err := wrapped.Generate(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !has(seen[1], "mcp__slack__send_message") || has(seen[1], "mcp__github__create_issue") {
		t.Errorf("second call bound %v", seen[1])
	}
}

func TestWrapModelWithoutExternalToolsHidesToolSearch(t *testing.T) {
	r := newReg(t)
	s := r.NewSession()
	var infos []*schema.ToolInfo
	for _, bt := range s.Executable() {
		info, _ := bt.Info(context.Background())
		infos = append(infos, info)
	}
	var seen [][]string
	wrapped, _ := s.WrapModel(&recModel{seen: &seen}).WithTools(infos)
	if _, err := wrapped.Generate(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	for _, n := range seen[0] {
		if n == ToolSearchName {
			t.Errorf("tool_search bound with no deferred tools: %v", seen[0])
		}
	}
	if len(seen[0]) != 4 {
		t.Errorf("want the 4 other built-ins bound, got %v", seen[0])
	}
}
