package prompt

import (
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
