package prompt

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func baseCtx() TurnContext {
	return TurnContext{
		Now:           time.Date(2026, 9, 10, 14, 30, 0, 0, time.UTC),
		Provider:      "claude",
		Model:         "claude-sonnet-5",
		SessionID:     "sess-1",
		Identity:      "You are bepilot.",
		ResponseStyle: "Be concise.",
		Tools:         map[string]string{"load_skill": "load a skill", "current_time": "now"},
	}
}

func TestBuildIncludesEnvironmentAndStyle(t *testing.T) {
	out := Build(baseCtx())
	for _, want := range []string{"You are bepilot.", "<environment>", "Thursday, 10 September 2026", "claude-sonnet-5", "Tools available:", "<response_style>", "Be concise."} {
		if !strings.Contains(out, want) {
			t.Fatalf("prompt missing %q:\n%s", want, out)
		}
	}
}

func TestSkillIndexRendersRetrievedSkills(t *testing.T) {
	tc := baseCtx()
	tc.Skills = []SkillRef{{Slug: "pdf-forms", Name: "PDF Forms", Description: "Fill and flatten PDF forms", Score: 0.7}}
	out := Build(tc)
	if !strings.Contains(out, "<skills>") || !strings.Contains(out, "slug: pdf-forms") {
		t.Fatalf("skill index not rendered:\n%s", out)
	}
	if !strings.Contains(out, "load_skill") {
		t.Fatalf("skill index should instruct load_skill:\n%s", out)
	}
}

func TestSkillIndexOmittedWhenEmpty(t *testing.T) {
	if strings.Contains(Build(baseCtx()), "<skills>") {
		t.Fatal("skills section should be omitted when no skills retrieved")
	}
}

func TestSystemOverrideAppended(t *testing.T) {
	tc := baseCtx()
	tc.SystemOverride = "Always answer in French."
	out := Build(tc)
	if !strings.Contains(out, "<session_instructions>") || !strings.Contains(out, "French") {
		t.Fatalf("override not appended:\n%s", out)
	}
}

func TestDeferredToolsSection(t *testing.T) {
	none := Build(TurnContext{Now: time.Now(), Tools: map[string]string{"current_time": "x"}})
	if strings.Contains(none, "deferred_tools") || strings.Contains(none, "tool_search") {
		t.Errorf("no deferred tools: section must be omitted entirely:\n%s", none)
	}

	with := Build(TurnContext{Now: time.Now(), DeferredTools: []string{"mcp__a__b", "mcp__a__c"}})
	for _, want := range []string{"<deferred_tools>", "tool_search", "- mcp__a__b", "- mcp__a__c"} {
		if !strings.Contains(with, want) {
			t.Errorf("missing %q in:\n%s", want, with)
		}
	}

	many := make([]string, maxListedDeferred+7)
	for i := range many {
		many[i] = fmt.Sprintf("mcp__s__t%04d", i)
	}
	capped := Build(TurnContext{Now: time.Now(), DeferredTools: many})
	if !strings.Contains(capped, "and 7 more") || strings.Contains(capped, many[len(many)-1]) {
		t.Errorf("cap not applied")
	}
}

func TestAccuracySection(t *testing.T) {
	// Without the tools the rules still apply, but no tool is named.
	plain := Build(baseCtx())
	if !strings.Contains(plain, "<accuracy>") || !strings.Contains(plain, "NEVER put an expression") {
		t.Fatalf("JSON/arithmetic rules must always be present:\n%s", plain)
	}
	if strings.Contains(plain, "calculate tool") || strings.Contains(plain, "json_validate") {
		t.Errorf("tools not bound this turn must not be mentioned:\n%s", plain)
	}

	tc := baseCtx()
	tc.Tools["calculate"] = "math"
	tc.Tools["json_validate"] = "json"
	with := Build(tc)
	for _, want := range []string{"calculate tool", "json_validate"} {
		if !strings.Contains(with, want) {
			t.Errorf("missing %q when the tool is bound:\n%s", want, with)
		}
	}
}
