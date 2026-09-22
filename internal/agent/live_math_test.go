package agent

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"

	"github.com/thanhenti/bepilot/internal/agent/events"
	"github.com/thanhenti/bepilot/internal/agent/prompt"
	"github.com/thanhenti/bepilot/internal/config"
	"github.com/thanhenti/bepilot/internal/tools"
)

// liveStatementMarkdown is a simulated financial-statement extraction. The
// closing cash on the cash-flow statement is deliberately 1,000,000 higher than
// opening cash + net change, so exactly one reconciliation must fail.
const liveStatementMarkdown = `# BÁO CÁO TÀI CHÍNH HỢP NHẤT — CÔNG TY CP ABC
Kỳ báo cáo: Năm 2025 (đơn vị: VND)

## Bảng cân đối kế toán (31/12/2025)

| Mã số | Chỉ tiêu | Số cuối kỳ |
|---|---|---:|
| 100 | A. Tài sản ngắn hạn | 658558466616 |
| 200 | B. Tài sản dài hạn | 33142036070 |
| 270 | TỔNG TÀI SẢN | 691700502686 |
| 300 | C. Nợ phải trả | 525990687369 |
| 400 | D. Vốn chủ sở hữu | 165709815317 |
| 440 | TỔNG NGUỒN VỐN | 691700502686 |

## Kết quả hoạt động kinh doanh

| Mã số | Chỉ tiêu | Năm 2025 |
|---|---|---:|
| 01 | Doanh thu bán hàng | 1547961374470 |
| 02 | Các khoản giảm trừ | 0 |
| 10 | Doanh thu thuần | 1547961374470 |
| 11 | Giá vốn hàng bán | 1396622149445 |
| 20 | Lợi nhuận gộp | 151339225025 |
| 21 | Doanh thu tài chính | 3042642417 |
| 22 | Chi phí tài chính | 29764305468 |
| 25 | Chi phí bán hàng | 49856366672 |
| 26 | Chi phí quản lý | 60745156333 |
| 30 | Lợi nhuận thuần từ HĐKD | 14016038969 |
| 31 | Thu nhập khác | 1182052553 |
| 32 | Chi phí khác | 5612508553 |
| 40 | Lợi nhuận khác | -4430456000 |
| 50 | Tổng lợi nhuận trước thuế | 9585582969 |
| 51 | Thuế TNDN hiện hành | 1917116594 |
| 52 | Thuế TNDN hoãn lại | 0 |
| 60 | Lợi nhuận sau thuế | 7668466375 |

## Lưu chuyển tiền tệ

| Chỉ tiêu | Số tiền |
|---|---:|
| Lưu chuyển tiền thuần trong kỳ | 1386477364 |
| Tiền đầu kỳ | 26510634750 |
| Ảnh hưởng tỷ giá | 0 |
| Tiền cuối kỳ | 27898112114 |
`

const liveStatementRequest = `Dưới đây là báo cáo tài chính ở dạng Markdown. Hãy parse nó sang JSON và kiểm tra các công thức kế toán sau, mỗi công thức là một phần tử trong mảng "checks":

1. Tổng tài sản (270) = Tài sản ngắn hạn (100) + Tài sản dài hạn (200)
2. Tổng nguồn vốn (440) = Nợ phải trả (300) + Vốn chủ sở hữu (400)
3. Doanh thu thuần (10) = Doanh thu (01) − Các khoản giảm trừ (02)
4. Lợi nhuận gộp (20) = Doanh thu thuần (10) − Giá vốn (11)
5. Lợi nhuận thuần từ HĐKD (30) = 20 + 21 − 22 − 25 − 26
6. Lợi nhuận khác (40) = Thu nhập khác (31) − Chi phí khác (32)
7. Tổng LN trước thuế (50) = 30 + 40
8. LNST (60) = 50 − 51 − 52
9. Tiền thuần trong kỳ + Tiền đầu kỳ + Ảnh hưởng tỷ giá = Tiền cuối kỳ

Mỗi check có các trường: "check" (mô tả), "passed" (boolean), "expected" (số vế công thức tính ra), "actual" (số ghi trên báo cáo), "difference" (actual − expected). Trả về JSON.

` + "```markdown\n" + liveStatementMarkdown + "```"

type liveCheck struct {
	Check      string  `json:"check"`
	Passed     bool    `json:"passed"`
	Expected   float64 `json:"expected"`
	Actual     float64 `json:"actual"`
	Difference float64 `json:"difference"`
}

// TestLiveMathAndJSON drives the real system prompt, real tool registry and the
// real ReAct loop against an actual model. It needs an OpenAI-compatible
// endpoint whose loaded context window fits roughly 12k tokens, so it only runs
// when BEPILOT_LIVE_LLM=1:
//
//	set -a; source .env; set +a
//	BEPILOT_LIVE_LLM=1 go test ./internal/agent -run TestLiveMathAndJSON -v -timeout 15m
func TestLiveMathAndJSON(t *testing.T) {
	if os.Getenv("BEPILOT_LIVE_LLM") != "1" {
		t.Skip("set BEPILOT_LIVE_LLM=1 to run against a live model")
	}
	cfg, err := config.Load("../../configs/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	maxTok := 8192
	cm, err := openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{
		APIKey:    "sk-no-key",
		BaseURL:   strings.TrimSuffix(os.Getenv("OPENAI_BASE_URL"), "/"),
		Model:     os.Getenv("BEPILOT_DEFAULT_MODEL"),
		MaxTokens: &maxTok,
		Timeout:   10 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	reg, err := tools.NewRegistry(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := reg.NewSession()
	ctx = tools.WithSession(ctx, sess)

	sys := prompt.Build(prompt.TurnContext{
		Now:           time.Now(),
		Provider:      "openai",
		Model:         os.Getenv("BEPILOT_DEFAULT_MODEL"),
		Identity:      cfg.Agent.Identity,
		ResponseStyle: cfg.Agent.ResponseStyle,
		Tools:         sess.VisibleDescriptions(),
	})
	ra, err := buildReactAgent(ctx, sess.WrapModel(cm), sess.Executable(), sys, cfg.Agent.MaxSteps, nil)
	if err != nil {
		t.Fatal(err)
	}

	var evs []events.Event
	asm := newAssembler(func(e events.Event) { evs = append(evs, e) })
	history := []*einoMessage{{Role: schema.User, Content: liveStatementRequest}}
	if err := (&Agent{}).drive(ctx, ra, history, testCallbackHandler(asm)); err != nil {
		t.Fatalf("drive: %v", err)
	}

	toolCalls := map[string]int{}
	for _, e := range evs {
		switch e.Kind {
		case events.KindToolExecStart:
			toolCalls[e.ToolName]++
			t.Logf("TOOL CALL %s %s", e.ToolName, e.ToolInput)
		case events.KindToolExecStop:
			t.Logf("TOOL RESULT %s: %v", e.ToolName, e.ToolResult)
		}
	}
	blocks, _ := asm.result("end_turn")
	var answer string
	for _, b := range blocks {
		if b.Type == "text" {
			answer += b.Text
		}
	}
	t.Logf("FINAL ANSWER:\n%s", answer)

	if toolCalls["calculate"] == 0 {
		t.Errorf("the model never called calculate (calls: %v)", toolCalls)
	}

	if strings.TrimSpace(answer) == "" {
		t.Fatalf("the model produced no final answer after %v tool calls; if the server reports stop_reason max_tokens, the model's loaded context window is too small for this prompt (LM Studio defaults to 4-8k) — reload it with a context of at least 16k", toolCalls)
	}
	raw := extractJSON(answer)
	if !json.Valid([]byte(raw)) {
		t.Fatalf("final answer does not contain valid JSON:\n%s", raw)
	}
	var doc struct {
		Checks []liveCheck `json:"checks"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("checks not parseable as numbers/booleans: %v", err)
	}

	// Independent ground truth, computed here with big integers.
	type want struct{ expected, actual int64 }
	wants := []want{
		{658558466616 + 33142036070, 691700502686},
		{525990687369 + 165709815317, 691700502686},
		{1547961374470 - 0, 1547961374470},
		{1547961374470 - 1396622149445, 151339225025},
		{151339225025 + 3042642417 - 29764305468 - 49856366672 - 60745156333, 14016038969},
		{1182052553 - 5612508553, -4430456000},
		{14016038969 + -4430456000, 9585582969},
		{9585582969 - 1917116594 - 0, 7668466375},
		{1386477364 + 26510634750 + 0, 27898112114},
	}
	if len(doc.Checks) != len(wants) {
		t.Fatalf("got %d checks, want %d", len(doc.Checks), len(wants))
	}
	for i, w := range wants {
		got := doc.Checks[i]
		diff := new(big.Int).Sub(big.NewInt(w.actual), big.NewInt(w.expected)).Int64()
		if int64(got.Expected) != w.expected || int64(got.Actual) != w.actual ||
			int64(got.Difference) != diff || got.Passed != (diff == 0) {
			t.Errorf("check %d wrong: got %+v, want expected=%d actual=%d difference=%d passed=%v",
				i+1, got, w.expected, w.actual, diff, diff == 0)
		}
	}
}

// extractJSON returns the JSON object in an answer, whether or not the model
// wrapped it in a Markdown fence.
func extractJSON(s string) string {
	if i := strings.Index(s, "```json"); i >= 0 {
		s = s[i+len("```json"):]
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
		return strings.TrimSpace(s)
	}
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j < i {
		return s
	}
	return s[i : j+1]
}
