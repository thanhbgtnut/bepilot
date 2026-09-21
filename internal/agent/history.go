package agent

import (
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/thanhenti/bepilot/internal/domain"
)

// approxTokens is a cheap heuristic (~4 chars/token) used only for history
// trimming, never for billing.
func approxTokens(s string) int { return (len(s) + 3) / 4 }

// historyToMessages converts stored messages into eino messages. An assistant
// message that carried tool_use / tool_result blocks is expanded into an
// assistant message with ToolCalls followed by one tool message per result, so
// the model sees a well-formed transcript.
func historyToMessages(msgs []domain.Message) []*schema.Message {
	var out []*schema.Message
	for _, m := range msgs {
		switch m.Role {
		case domain.RoleUser:
			out = append(out, &schema.Message{Role: schema.User, Content: blocksToText(m.Blocks)})

		case domain.RoleAssistant:
			am := &schema.Message{Role: schema.Assistant}
			var toolMsgs []*schema.Message
			var text strings.Builder
			for _, b := range m.Blocks {
				switch b.Type {
				case domain.BlockText:
					text.WriteString(b.Text)
				case domain.BlockToolUse:
					idx := len(am.ToolCalls)
					am.ToolCalls = append(am.ToolCalls, schema.ToolCall{
						Index:    &idx,
						ID:       b.ToolUseID,
						Type:     "function",
						Function: schema.FunctionCall{Name: b.ToolName, Arguments: mustJSON(b.ToolInput)},
					})
				case domain.BlockToolResult:
					toolMsgs = append(toolMsgs, &schema.Message{
						Role:       schema.Tool,
						ToolCallID: b.ToolUseID,
						ToolName:   b.ToolName,
						Content:    toolResultText(b.ToolResult),
					})
				}
			}
			am.Content = text.String()
			am.ToolCalls, toolMsgs = pairToolCalls(am.ToolCalls, toolMsgs)
			if am.Content == "" && len(am.ToolCalls) == 0 && len(toolMsgs) == 0 {
				continue
			}
			out = append(out, am)
			out = append(out, toolMsgs...)
		}
	}
	return out
}

// pairToolCalls keeps only the tool calls that have a stored result, and only
// the results that have a call. A turn that was interrupted, cancelled or failed
// while a tool ran leaves a tool_use with no tool_result; sending that back
// makes the provider reject the whole conversation ("tool_use ids were found
// without tool_result blocks"), which would wedge the session for good.
func pairToolCalls(calls []schema.ToolCall, results []*schema.Message) ([]schema.ToolCall, []*schema.Message) {
	have := make(map[string]bool, len(results))
	for _, r := range results {
		have[r.ToolCallID] = true
	}
	var keptCalls []schema.ToolCall
	called := make(map[string]bool, len(calls))
	for _, c := range calls {
		if have[c.ID] {
			idx := len(keptCalls)
			c.Index = &idx
			keptCalls = append(keptCalls, c)
			called[c.ID] = true
		}
	}
	var keptResults []*schema.Message
	for _, r := range results {
		if called[r.ToolCallID] {
			keptResults = append(keptResults, r)
		}
	}
	return keptCalls, keptResults
}

// trimHistory keeps the most recent messages within a token budget. It never
// splits an assistant/tool group: trimming happens at message boundaries from
// the front. The most recent user message is always kept, even when it alone
// exceeds the budget, and the result always begins with a user message —
// otherwise the model would receive only the system prompt (Claude rejects that
// with "only system message in input, require at least 1 user message").
func trimHistory(msgs []*schema.Message, budget int) []*schema.Message {
	if budget <= 0 {
		return msgs
	}
	total := 0
	for _, m := range msgs {
		total += approxTokens(m.Content)
		for _, tc := range m.ToolCalls {
			total += approxTokens(tc.Function.Arguments)
		}
	}
	if total <= budget {
		return msgs
	}
	// Drop from the front until under budget, but never drop a leading tool
	// message (would orphan it); skip forward to the next user message.
	lastUser := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == schema.User {
			lastUser = i
			break
		}
	}
	if lastUser < 0 {
		return msgs
	}
	start := 0
	for start < lastUser && total > budget {
		total -= approxTokens(msgs[start].Content)
		for _, tc := range msgs[start].ToolCalls {
			total -= approxTokens(tc.Function.Arguments)
		}
		start++
	}
	// Never begin on an orphaned tool result or a dangling assistant reply.
	for start < lastUser && msgs[start].Role != schema.User {
		start++
	}
	return msgs[start:]
}

func blocksToText(blocks []domain.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == domain.BlockText {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

// lastUserText returns the text of the most recent user message.
func lastUserText(msgs []domain.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == domain.RoleUser {
			return blocksToText(msgs[i].Blocks)
		}
	}
	return ""
}
