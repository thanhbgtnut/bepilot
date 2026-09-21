// Package prompt assembles the dynamic system prompt for each agent turn. The
// prompt is rebuilt every turn from ordered sections so that time-sensitive
// context (the current date, the retrieved skills, the rolling summary) is
// always fresh — mirroring how Claude injects an environment block and a
// skill index without the user having to ask.
package prompt

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// SkillRef is a retrieved skill surfaced to the model.
type SkillRef struct {
	Slug        string
	Name        string
	Description string
	Score       float64
}

// ContextItem is one client-supplied situational note (AG-UI's `context`
// array): a labeled piece of information about the caller's environment,
// distinct from the conversation itself.
type ContextItem struct {
	Description string
	Value       string
}

// TurnContext is everything a section might need. It is assembled by the runner
// before each model call.
type TurnContext struct {
	// MaxSteps is the most model calls this turn may make; 0 = not stated.
	MaxSteps int

	Now       time.Time
	Provider  string
	Model     string
	SessionID string

	UserName string
	Locale   string

	Identity       string
	ResponseStyle  string
	SystemOverride string // per-session override/extension
	Summary        string // rolling summary of older turns

	Tools map[string]string // name -> description of tools bound to the model this turn
	// DeferredTools are external tools (MCP etc.) whose schemas are not loaded;
	// only their names are shown and the model must load one with tool_search.
	DeferredTools []string
	Skills        []SkillRef
	Memory        []string

	Context []ContextItem // client-supplied situational notes (AG-UI `context`)
	State   string        // client-supplied state, pre-serialized JSON (AG-UI `state`)
}

// Section produces one block of the system prompt, or "" to omit it.
type Section func(TurnContext) string

// DefaultSections is the ordered pipeline used by the agent.
var DefaultSections = []Section{
	sectionIdentity,
	sectionEnvironment,
	sectionClientContext,
	sectionClientState,
	sectionMemory,
	sectionToolGuidance,
	sectionAccuracy,
	sectionDeferredTools,
	sectionSkillIndex,
	sectionResponseStyle,
	sectionSystemOverride,
}

// Build runs the section pipeline and joins the non-empty results.
func Build(tc TurnContext, sections ...Section) string {
	if len(sections) == 0 {
		sections = DefaultSections
	}
	parts := make([]string, 0, len(sections))
	for _, s := range sections {
		if out := strings.TrimSpace(s(tc)); out != "" {
			parts = append(parts, out)
		}
	}
	return strings.Join(parts, "\n\n")
}

func sectionIdentity(tc TurnContext) string {
	if tc.Identity == "" {
		return ""
	}
	return tc.Identity
}

func sectionEnvironment(tc TurnContext) string {
	var b strings.Builder
	b.WriteString("<environment>\n")
	fmt.Fprintf(&b, "Current date: %s\n", tc.Now.Format("Monday, 2 January 2006"))
	fmt.Fprintf(&b, "Current time: %s\n", tc.Now.Format("15:04 MST"))
	if tc.Model != "" {
		fmt.Fprintf(&b, "Model: %s (provider: %s)\n", tc.Model, tc.Provider)
	}
	if tc.SessionID != "" {
		fmt.Fprintf(&b, "Session: %s\n", tc.SessionID)
	}
	if tc.UserName != "" {
		fmt.Fprintf(&b, "User: %s\n", tc.UserName)
	}
	if tc.Locale != "" {
		fmt.Fprintf(&b, "User locale: %s\n", tc.Locale)
	}
	if len(tc.Tools) > 0 {
		names := make([]string, 0, len(tc.Tools))
		for n := range tc.Tools {
			names = append(names, n)
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "Tools available: %s\n", strings.Join(names, ", "))
	}
	b.WriteString("</environment>")
	return b.String()
}

func sectionClientContext(tc TurnContext) string {
	if len(tc.Context) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<client_context>\n")
	b.WriteString("The client application supplied this situational context for the current turn:\n")
	for _, c := range tc.Context {
		desc, val := strings.TrimSpace(c.Description), strings.TrimSpace(c.Value)
		switch {
		case desc == "" && val == "":
			continue
		case desc == "":
			fmt.Fprintf(&b, "- %s\n", val)
		default:
			fmt.Fprintf(&b, "- %s: %s\n", desc, val)
		}
	}
	b.WriteString("</client_context>")
	return b.String()
}

func sectionClientState(tc TurnContext) string {
	if strings.TrimSpace(tc.State) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<client_state>\n")
	b.WriteString("The client application's current state (JSON), for reference only — it is not part of the conversation and you cannot write to it directly:\n")
	b.WriteString(strings.TrimSpace(tc.State))
	b.WriteString("\n</client_state>")
	return b.String()
}

func sectionMemory(tc TurnContext) string {
	if len(tc.Memory) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<memory>\nRelevant notes from earlier in this conversation:\n")
	for _, m := range tc.Memory {
		fmt.Fprintf(&b, "- %s\n", m)
	}
	if strings.TrimSpace(tc.Summary) != "" {
		fmt.Fprintf(&b, "\nSummary of earlier turns:\n%s\n", strings.TrimSpace(tc.Summary))
	}
	b.WriteString("</memory>")
	return b.String()
}

func sectionToolGuidance(tc TurnContext) string {
	if len(tc.Tools) == 0 {
		return ""
	}
	guide := strings.TrimSpace(`
<tool_use>
- Call a tool only when it materially improves the answer (fresh data, a computation, reading a specific URL, or loading a skill). For things you already know, answer directly.
- When several independent tool calls are needed, request them together rather than one at a time.
- Never invent tool output. If a tool errors or returns nothing useful, say so and proceed with your best effort.
- Stop calling tools once you have what you need, then give the final answer.
</tool_use>`)
	if tc.MaxSteps > 0 {
		// Said up front so the model plans for it, rather than meeting the limit
		// as a surprise on its last call.
		guide = strings.Replace(guide, "</tool_use>", fmt.Sprintf("- A turn allows at most %d model calls (every round of tool use is one) and the last has no tools. Plan for that: do the essential work first and leave room for a complete final answer.\n</tool_use>", tc.MaxSteps), 1)
	}
	return guide
}

// sectionAccuracy tells the model how to keep numbers and machine-readable
// output correct. Its arithmetic and JSON rules apply with or without tools; the
// tool names are only mentioned when they are bound this turn.
func sectionAccuracy(tc TurnContext) string {
	_, hasCalc := tc.Tools["calculate"]
	_, hasJSON := tc.Tools["json_validate"]

	var b strings.Builder
	b.WriteString("<accuracy>\n")
	if hasCalc {
		b.WriteString("- Never add, subtract, multiply, divide or compare multi-digit numbers in your head. Evaluate them with the calculate tool — batch every expression you need into one call — and copy its results. This includes totals, differences, percentages and every \"expected vs actual\" check.\n")
		b.WriteString("- For a check, put the formula itself in calculate (e.g. `b + c`) and copy that value as \"expected\"; put `actual - expected` (with the numbers substituted) in the same call and copy it as \"difference\", keeping the sign the user defined. Do not evaluate only the residual: it hides the expected value and flips signs.\n")
	} else {
		b.WriteString("- Work through multi-digit arithmetic step by step and re-check each result before stating it; do not guess totals.\n")
	}
	b.WriteString("- When the user wants JSON (or any output a program will parse), emit strictly valid JSON: every value is a literal (number, string, true/false/null, object, array). NEVER put an expression such as `a + b` or `x - y` where a value goes — do the arithmetic first and write the resulting number. No comments, trailing commas, single quotes, or thousand separators in numbers (write 1547961374470, not 1.547.961.374.470). Do not add prose inside the JSON.\n")
	if hasJSON {
		b.WriteString("- Before sending a long or computed JSON document, run it through json_validate and fix whatever it reports.\n")
	}
	b.WriteString("- If asked for a check or reconciliation, report the computed values and the difference you actually calculated; only mark it passed when the difference is zero.\n")
	b.WriteString("</accuracy>")
	return b.String()
}

// maxListedDeferred caps how many deferred tool names are spelled out in the
// prompt; the rest are still reachable through tool_search.
const maxListedDeferred = 300

func sectionDeferredTools(tc TurnContext) string {
	if len(tc.DeferredTools) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(`
<deferred_tools>
More tools are available, but their schemas are not loaded, so you cannot call them yet. To use one: call tool_search first — with "select:<name>[,<name>...]" for exact names below, or with keywords (prefix a keyword with + to require it in the name) — then call the loaded tool on your next step. Never guess a deferred tool's arguments; use the input_schema tool_search returns.
`))
	b.WriteString("\n")
	names := tc.DeferredTools
	extra := 0
	if len(names) > maxListedDeferred {
		extra = len(names) - maxListedDeferred
		names = names[:maxListedDeferred]
	}
	for _, n := range names {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	if extra > 0 {
		fmt.Fprintf(&b, "- … and %d more (find them with tool_search keywords)\n", extra)
	}
	b.WriteString("</deferred_tools>")
	return b.String()
}

func sectionSkillIndex(tc TurnContext) string {
	if len(tc.Skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(`
<skills>
These skills look relevant to the current conversation. A skill is a packaged procedure for a class of task. If the user's request falls under one, call load_skill with its slug to get the full instructions BEFORE you start — even if the user did not mention the skill by name. If none actually apply, ignore this list.
`))
	b.WriteString("\n")
	for _, s := range tc.Skills {
		fmt.Fprintf(&b, "- %s (slug: %s) — %s\n", s.Name, s.Slug, oneLine(s.Description))
	}
	b.WriteString("</skills>")
	return b.String()
}

func sectionResponseStyle(tc TurnContext) string {
	if tc.ResponseStyle == "" {
		return ""
	}
	return "<response_style>\n" + strings.TrimSpace(tc.ResponseStyle) + "\n</response_style>"
}

func sectionSystemOverride(tc TurnContext) string {
	if strings.TrimSpace(tc.SystemOverride) == "" {
		return ""
	}
	return "<session_instructions>\n" + strings.TrimSpace(tc.SystemOverride) + "\n</session_instructions>"
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}
