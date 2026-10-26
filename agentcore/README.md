# agentcore

Module Go chứa phần agent của bepilot, dùng được ở project khác:

```
tin nhắn ─► lịch sử (cắt theo token budget) + tóm tắt ─► tìm skill liên quan
        ─► system prompt động ─► vòng ReAct của Eino (stream, tool, MCP)
        ─► sự kiện stream (Anthropic / AG-UI) ─► lưu tin nhắn + bản ghi lượt chạy
```

Module không có database, HTTP server hay file config. Nơi lưu hội thoại là interface `agent.Store`, nơi lưu skill là `skills.Repo`. `memstore` lưu cả hai trong bộ nhớ, nên agent chạy được ngay mà không cần Postgres. bepilot nối các interface này vào Postgres (`internal/store`) và Hertz (`internal/server`).

## Cài đặt

```
go get github.com/thanhenti/bepilot/agentcore
```

Module nằm trong thư mục con của repo bepilot, nên phiên bản được đánh tag dạng `agentcore/v0.1.0`. Repo private thì đặt `GOPRIVATE=github.com/thanhenti/*`. Khi làm việc trên cùng máy, có thể trỏ thẳng vào thư mục (bepilot cũng làm vậy trong `go.mod` gốc):

```
replace github.com/thanhenti/bepilot/agentcore => ../bepilot/agentcore
```

Yêu cầu Go 1.26.

## Dùng

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/thanhenti/bepilot/agentcore/agent"
	"github.com/thanhenti/bepilot/agentcore/agent/events"
	"github.com/thanhenti/bepilot/agentcore/domain"
	"github.com/thanhenti/bepilot/agentcore/llm"
	"github.com/thanhenti/bepilot/agentcore/memstore"
	"github.com/thanhenti/bepilot/agentcore/retrieval"
	"github.com/thanhenti/bepilot/agentcore/skills"
	"github.com/thanhenti/bepilot/agentcore/tools"
)

func main() {
	ctx := context.Background()

	// 1. Nơi lưu hội thoại và skill (ở đây: trong bộ nhớ)
	st := memstore.New()
	skillSvc := skills.NewService(memstore.NewSkills(), retrieval.NewHashEmbedder(256), "skills", nil)
	if _, err := skillSvc.Sync(ctx); err != nil { // đọc skills/<slug>/SKILL.md
		log.Fatal(err)
	}

	// 2. Tool có sẵn (calculate, json_validate, http_fetch, load_skill, tool_search…)
	toolReg, err := tools.NewRegistry(skillSvc, nil, nil)
	if err != nil {
		log.Fatal(err)
	}

	// 3. Model
	lcfg := llm.Config{
		DefaultProvider: "claude",
		MaxTokens:       4096,
		Providers: map[string]llm.ProviderConfig{
			"claude": {Kind: "claude", APIKey: os.Getenv("ANTHROPIC_API_KEY")},
		},
	}
	reg, err := llm.NewRegistry(lcfg)
	if err != nil {
		log.Fatal(err)
	}

	// 4. Agent
	ag := agent.New(st, reg, toolReg, skillSvc, agent.Config{MaxSteps: 16}, lcfg, nil)
	sess := st.CreateSession(domain.Session{Title: "demo"})

	out, err := ag.Run(ctx, agent.RunInput{
		Session:  sess,
		UserText: "Tính 17,5% của 1.234.567",
		Model:    "claude-sonnet-5-5",
	}, func(ev events.Event) {
		if ev.Kind == events.KindTextDelta {
			fmt.Print(ev.Text)
		}
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\nstop:", out.Stats.StopReason, "steps:", out.Stats.Steps)
}
```

Truyền `nil` thay cho sink thì lượt chạy không stream, kết quả nằm trong `out.AssistantMessage`.

## Kết quả

`Run` trả `agent.RunOutput`:

| Trường | Nội dung |
|---|---|
| `UserMessage`, `AssistantMessage` | hai tin nhắn đã lưu vào `Store` (`domain.Message`, các block text / thinking / tool_use / tool_result) |
| `Stats` | provider, model, `StopReason`, token vào/ra, số lần gọi model, độ trễ, skill tìm được, `Detail` (tool đã gọi, max_tokens, temperature…) |
| `Steered` | `true` khi session đang có lượt chạy: tin nhắn được đưa vào lượt đó, câu trả lời nằm trên stream của lượt đang chạy |

Tin nhắn của assistant luôn được lưu, kể cả khi model lỗi giữa chừng hoặc client ngắt kết nối. `Run` chỉ trả lỗi khi lượt chạy lỗi; phần đã sinh ra vẫn có trong `AssistantMessage`.

## Sự kiện stream

Sink nhận `events.Event` (`message_start`, `content_block_*`, `tool_execution_*`, `message_delta`, `message_stop`, `error`). Hai mapper đổi chúng thành frame SSE (`events.Frame`, chỉ gồm tên sự kiện và data, không phụ thuộc HTTP framework):

- `events.NewAnthropicMapper`: frame của Anthropic Messages API.
- `events.NewAGUIMapper`: frame của giao thức AG-UI.

## Thay thành phần

| Thành phần | Interface | Có sẵn |
|---|---|---|
| Lưu hội thoại | `agent.Store` | `memstore.Store`; bepilot: `store.Store.Agent()` (Postgres) |
| Tìm skill cho lượt chạy | `agent.SkillRetriever` | `skills.Service` |
| Lưu skill + embedding | `skills.Repo` | `memstore.Skills`; bepilot: `store.SkillsRepo` (pgvector) |
| Embedding | `retrieval.Embedder` | `retrieval.NewHashEmbedder` (offline), `retrieval.NewOpenAIEmbedder` |
| Model | `llm.Provider` | `claude`, `openai` (cả server tương thích OpenAI), `ark`, `fakeprovider` |
| Nạp skill cho `load_skill` | `tools.SkillLoader` | `skills.Service` |
| Lưu kết quả `save_task_result` | `tools.TaskResultWriter` | không có; truyền `nil` thì tool báo không dùng được |
| Tool ngoài | `tools.Registry.Register` (tool deferred) | `mcp.Manager` (MCP stdio / SSE / streamable HTTP) |

`Store` phải an toàn khi gọi đồng thời: tin nhắn gửi tới khi một lượt đang chạy được ghi vào cùng session cùng lúc với lượt đó.

## Hành vi chính

- Mỗi session chỉ chạy một lượt một lúc. Tin nhắn gửi tới giữa chừng không phải chờ: lượt đang chạy đọc nó trước lần gọi model kế tiếp. Tin nhắn ngắn kiểu `stop` / `dừng` / `hủy` ngắt lượt đang chạy, phần đã sinh được giữ lại.
- Câu trả lời bị cắt vì giới hạn output được tự viết tiếp (tối đa 4 lần mỗi lần gọi model).
- Prompt nêu ngân sách `MaxSteps`. Gần hết ngân sách thì model được nhắc, và lần gọi cuối không có tool nên luôn kết thúc bằng câu trả lời.
- Phép tính số học model để lại trong giá trị JSON được tính lại chính xác trước khi stream và lưu.
- Tool ngoài (MCP…) không gửi schema lên trước, model nạp bằng `tool_search` khi cần.
- `SummarizeEveryN > 0` thì tóm tắt hội thoại được làm mới ở nền sau mỗi N tin nhắn và đưa vào prompt.

## Test

```
go test ./...
```

`memstore.TestAgentRunsWithoutDatabase` chạy cả lượt với `memstore` và `fakeprovider`, không cần mạng hay database.
