package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/thanhenti/bepilot/internal/domain"
	"github.com/thanhenti/bepilot/internal/llm"
	"github.com/thanhenti/bepilot/internal/store"
)

// maybeSummarize refreshes the session's rolling summary in the background once
// the conversation crosses a multiple of SummarizeEveryN messages. The summary
// is fed into the system prompt on later turns so trimmed history is not lost.
func (a *Agent) maybeSummarize(sess domain.Session, msgCount int, provider llm.Provider, model string) {
	n := a.acfg.SummarizeEveryN
	if n <= 0 || msgCount < n || msgCount%n != 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		history, err := a.store.Messages.ListBySession(ctx, sess.ID, 0, 500)
		if err != nil {
			a.log.Warn("summary: load history failed", "err", err)
			return
		}
		var transcript strings.Builder
		for _, m := range history {
			transcript.WriteString(strings.ToUpper(m.Role))
			transcript.WriteString(": ")
			transcript.WriteString(blocksToText(m.Blocks))
			transcript.WriteString("\n")
		}

		summaryModel := a.lcfg.SummaryModel
		if summaryModel == "" {
			summaryModel = model
		}
		cm, err := provider.Model(ctx, summaryModel, llm.Options{MaxTokens: 512})
		if err != nil {
			a.log.Warn("summary: model failed", "err", err)
			return
		}
		msg, err := cm.Generate(ctx, []*schema.Message{
			schema.SystemMessage("Summarise the conversation so far in under 200 words. Capture decisions, facts established, the user's goal, and open threads. Write plain prose, no preamble."),
			schema.UserMessage(transcript.String()),
		})
		if err != nil || msg == nil {
			a.log.Warn("summary: generate failed", "err", err)
			return
		}
		summary := strings.TrimSpace(msg.Content)
		if summary == "" {
			return
		}
		if _, err := a.store.Sessions.Update(ctx, sess.ID, store.UpdateParams{Summary: &summary}); err != nil {
			a.log.Warn("summary: persist failed", "err", err)
			return
		}
		a.log.Info("session summary refreshed", "session", sess.ID, "chars", len(summary))
	}()
}

var _ = fmt.Sprintf
