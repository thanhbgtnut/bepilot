package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// continueNotice asks the model to pick up a reply that hit the output limit.
// It is sent as a user message rather than as an assistant prefill because not
// every provider (or newer model) accepts a transcript ending on the assistant.
const continueNotice = "[Notice from the system] Your previous reply was cut off by the output length limit. Continue it from the exact character where it stopped: no preamble, no apology, and do not repeat anything you already wrote. If you were inside a JSON document, a code block or a table, carry on inside it."

// maxContinuations bounds how many times one model call is resumed after being
// cut off, so a model that never finishes cannot loop forever.
const maxContinuations = 4

// hitOutputLimit reports whether a model call stopped because it ran out of
// output tokens (providers name this "length" or "max_tokens") with a plain
// text reply. A cut-off tool call is different — its arguments are unusable —
// and is left to the tool round to report.
func hitOutputLimit(finish string, sawToolCall bool) bool {
	if sawToolCall {
		return false
	}
	switch strings.ToLower(finish) {
	case "length", "max_tokens":
		return true
	}
	return false
}

func finishOf(m *schema.Message) string {
	if m == nil || m.ResponseMeta == nil {
		return ""
	}
	return m.ResponseMeta.FinishReason
}

// resumeTranscript is the conversation with the partial reply and the request
// to carry on appended.
func resumeTranscript(in []*schema.Message, partial string) []*schema.Message {
	out := make([]*schema.Message, 0, len(in)+2)
	out = append(out, in...)
	return append(out, schema.AssistantMessage(partial, nil), schema.UserMessage(continueNotice))
}

// generateResuming calls the model and, when the reply is cut off by the output
// limit, keeps asking it to continue and joins the pieces into one message. A
// long answer therefore arrives whole, whatever max_tokens is configured; the
// user never has to type "continue".
func generateResuming(ctx context.Context, m model.BaseChatModel, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	msg, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	text := msg.Content
	for i := 0; i < maxContinuations && hitOutputLimit(finishOf(msg), len(msg.ToolCalls) > 0) && ctx.Err() == nil; i++ {
		next, err := m.Generate(ctx, resumeTranscript(in, text), opts...)
		if err != nil {
			return nil, err
		}
		text += next.Content
		merged := *next
		merged.Content = text
		msg = &merged
	}
	return msg, nil
}

// streamResuming is generateResuming for a streamed call. Chunks are forwarded
// as they arrive, so the client sees one uninterrupted stream; when the first
// stream ends on the output limit, the continuation's chunks simply follow.
func streamResuming(ctx context.Context, m model.BaseChatModel, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	first, err := m.Stream(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](8)
	go func() {
		defer sw.Close()
		var text strings.Builder
		cur := first
		for round := 0; ; round++ {
			finish, sawTool, closed, err := forward(cur, sw, &text)
			if err != nil {
				sw.Send(nil, err)
				return
			}
			if closed || round >= maxContinuations || !hitOutputLimit(finish, sawTool) || ctx.Err() != nil {
				return
			}
			cur, err = m.Stream(ctx, resumeTranscript(in, text.String()), opts...)
			if err != nil {
				sw.Send(nil, err)
				return
			}
		}
	}()
	return sr, nil
}

// forward copies every chunk of src to dst, collecting the text and the last
// finish reason. closed reports that the consumer went away.
func forward(src *schema.StreamReader[*schema.Message], dst *schema.StreamWriter[*schema.Message], text *strings.Builder) (finish string, sawTool, closed bool, err error) {
	defer src.Close()
	for {
		chunk, rerr := src.Recv()
		if rerr != nil {
			if isEOF(rerr) {
				return finish, sawTool, false, nil
			}
			return finish, sawTool, false, rerr
		}
		if chunk == nil {
			continue
		}
		text.WriteString(chunk.Content)
		if len(chunk.ToolCalls) > 0 {
			sawTool = true
		}
		if f := finishOf(chunk); f != "" {
			finish = f
		}
		if dst.Send(chunk, nil) {
			return finish, sawTool, true, nil
		}
	}
}
