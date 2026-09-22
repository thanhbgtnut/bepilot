package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/cloudwego/eino/callbacks"
	einomodel "github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	ucb "github.com/cloudwego/eino/utils/callbacks"
)

// buildReactAgent constructs a ReAct agent that injects systemPrompt ahead of
// the conversation on every model call. returnDirectly names tools whose call
// ends the turn immediately instead of feeding a result back to the model —
// used for AG-UI client-executed tools, which bepilot cannot run itself.
//
// maxModelCalls is the most times the turn may call the model (agent.max_steps
// in the config). On the last one the model has no tools and must answer, so a
// turn that runs out of budget still ends with a reply rather than an error.
//
// steer, when non-nil, is the inbox of messages the user sends while the turn is
// running; the model reads them before its next step.
func buildReactAgent(ctx context.Context, cm einomodel.ToolCallingChatModel, tools []einotool.BaseTool, systemPrompt string, maxModelCalls int, returnDirectly map[string]struct{}, steer ...*steerer) (*react.Agent, error) {
	if maxModelCalls <= 0 {
		maxModelCalls = 16
	}
	var inbox *steerer
	if len(steer) > 0 {
		inbox = steer[0]
	}
	return react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel:   newBudgetModel(cm, maxModelCalls, inbox),
		ToolsConfig:        compose.ToolsNodeConfig{Tools: softenToolErrors(tools)},
		ToolReturnDirectly: returnDirectly,
		MaxStep:            graphStepsFor(maxModelCalls),
		MessageModifier: func(_ context.Context, input []*schema.Message) []*schema.Message {
			out := make([]*schema.Message, 0, len(input)+1)
			out = append(out, schema.SystemMessage(systemPrompt))
			return append(out, input...)
		},
		// The default checker only inspects the first chunk, which fails for
		// models (Claude) that emit text before tool calls. This drains the
		// whole stream and reports whether any tool call appeared.
		StreamToolCallChecker: bufferedToolCallChecker,
	})
}

// softenToolErrors wraps every tool so a failed call (bad arguments, a
// not-found skill slug, a network error, ...) becomes a normal tool result
// the model can read and react to, instead of a hard error. Without this,
// eino's ToolsNode.Invoke treats any non-interrupt tool error as fatal and
// aborts the whole ReAct run — one bad load_skill slug would end the entire
// turn with RUN_ERROR rather than letting the model see "not found" and try
// something else or answer without it.
func softenToolErrors(tools []einotool.BaseTool) []einotool.BaseTool {
	out := make([]einotool.BaseTool, len(tools))
	for i, t := range tools {
		out[i] = toolutils.WrapToolWithErrorHandler(t, func(_ context.Context, err error) string {
			return "error: " + err.Error()
		})
	}
	return out
}

func bufferedToolCallChecker(_ context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
	defer sr.Close()
	for {
		msg, err := sr.Recv()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if len(msg.ToolCalls) > 0 {
			return true, nil
		}
	}
}

// newCallbackHandler wires eino model/tool callbacks into the assembler. Model
// output streams are consumed synchronously so that a model call's content
// blocks are fully closed before the tool node runs.
//
// log receives one line per model call giving the approximate size of the
// transcript being sent, tagged with modelID. A turn that loops through many
// tool rounds keeps re-sending the whole transcript so far on every call, and
// nothing else in the run records how large that got — when a call fails
// against an upstream with a context or request-size limit tighter than
// expected, this is what lets that be told apart from a genuinely transient
// failure after the fact, from the log alone.
func newCallbackHandler(a *assembler, log *slog.Logger, modelID string) callbacks.Handler {
	model := &ucb.ModelCallbackHandler{
		OnStart: func(ctx context.Context, _ *callbacks.RunInfo, in *einomodel.CallbackInput) context.Context {
			if in != nil {
				log.Debug("model call", "model", modelID, "messages", len(in.Messages), "approx_tokens", approxMessagesTokens(in.Messages))
			}
			return ctx
		},
		OnEndWithStreamOutput: func(ctx context.Context, _ *callbacks.RunInfo, out *schema.StreamReader[*einomodel.CallbackOutput]) context.Context {
			defer out.Close()
			var finish string
			var usage *schema.TokenUsage
			for {
				chunk, err := out.Recv()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					a.fail("api_error", err.Error())
					break
				}
				if chunk == nil {
					continue
				}
				if chunk.Message != nil {
					a.modelChunk(chunk.Message)
					if chunk.Message.ResponseMeta != nil {
						if chunk.Message.ResponseMeta.FinishReason != "" {
							finish = chunk.Message.ResponseMeta.FinishReason
						}
						if chunk.Message.ResponseMeta.Usage != nil {
							usage = chunk.Message.ResponseMeta.Usage
						}
					}
				}
				if chunk.TokenUsage != nil {
					usage = &schema.TokenUsage{
						PromptTokens:     chunk.TokenUsage.PromptTokens,
						CompletionTokens: chunk.TokenUsage.CompletionTokens,
						TotalTokens:      chunk.TokenUsage.TotalTokens,
					}
				}
			}
			a.modelCallDone(finish, usage)
			return ctx
		},
		OnEnd: func(ctx context.Context, _ *callbacks.RunInfo, out *einomodel.CallbackOutput) context.Context {
			// Non-streaming model call (should not happen on the Stream path,
			// but handle it for completeness).
			if out == nil || out.Message == nil {
				return ctx
			}
			a.modelChunk(out.Message)
			var finish string
			var usage *schema.TokenUsage
			if out.Message.ResponseMeta != nil {
				finish = out.Message.ResponseMeta.FinishReason
				usage = out.Message.ResponseMeta.Usage
			}
			if out.TokenUsage != nil {
				usage = &schema.TokenUsage{
					PromptTokens:     out.TokenUsage.PromptTokens,
					CompletionTokens: out.TokenUsage.CompletionTokens,
					TotalTokens:      out.TokenUsage.TotalTokens,
				}
			}
			a.modelCallDone(finish, usage)
			return ctx
		},
		OnError: func(ctx context.Context, _ *callbacks.RunInfo, err error) context.Context {
			a.fail("api_error", err.Error())
			return ctx
		},
	}

	toolH := &ucb.ToolCallbackHandler{
		OnStart: func(ctx context.Context, info *callbacks.RunInfo, in *einotool.CallbackInput) context.Context {
			name := ""
			if info != nil {
				name = info.Name
			}
			args := ""
			if in != nil {
				args = in.ArgumentsInJSON
			}
			a.toolStart(name, args)
			return ctx
		},
		OnEnd: func(ctx context.Context, info *callbacks.RunInfo, out *einotool.CallbackOutput) context.Context {
			name := ""
			if info != nil {
				name = info.Name
			}
			resp := ""
			if out != nil {
				resp = out.Response
			}
			a.toolEnd(name, resp, false)
			return ctx
		},
		OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			name := ""
			if info != nil {
				name = info.Name
			}
			a.toolEnd(name, "error: "+err.Error(), true)
			return ctx
		},
	}

	return ucb.NewHandlerHelper().ChatModel(model).Tool(toolH).Handler()
}
