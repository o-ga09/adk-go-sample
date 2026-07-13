package gmailagent

import (
	"strings"
	"testing"
)

// TestInstructionRender guards the text/template migration: the three
// placeholders must be substituted (no literal {{...}} left) and the
// Git-managed user memory (#12) must be injected into every rendered
// instruction.
func TestInstructionRender(t *testing.T) {
	var buf strings.Builder
	if err := instruction.Execute(&buf, instructionData{
		GmailQuery: "is:unread newer_than:1d",
		ActionMode: "dry_run",
		UserMemory: "# ユーザーについて\n- 職種: Go エンジニア",
	}); err != nil {
		t.Fatalf("render instruction: %v", err)
	}
	got := buf.String()

	for _, want := range []string{
		`query="is:unread newer_than:1d"`,
		"動作モード: dry_run",
		"# ユーザーについて",
		"職種: Go エンジニア",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered instruction missing %q", want)
		}
	}
	if strings.Contains(got, "{{") || strings.Contains(got, "}}") {
		t.Errorf("rendered instruction still contains an unresolved template marker:\n%s", got)
	}
}

// TestUserMemoryEmbedded verifies memory.md is embedded and non-empty, so a
// deploy never ships an agent that silently forgot the owner's profile.
func TestUserMemoryEmbedded(t *testing.T) {
	if strings.TrimSpace(userMemory) == "" {
		t.Fatal("embedded memory.md is empty")
	}
}
