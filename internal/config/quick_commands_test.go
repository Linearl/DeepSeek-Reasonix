package config

import "testing"

func TestSetQuickCommandsValidatesAndDropsBlanks(t *testing.T) {
	var c Config
	if err := c.SetQuickCommands([]QuickCommandEntry{
		{Title: "   ", Text: "  "},
		{Title: " Summary ", Text: "summarize this"},
	}); err != nil {
		t.Fatalf("valid list rejected: %v", err)
	}
	got := c.DesktopQuickCommands()
	if len(got) != 1 || got[0].Title != "Summary" || got[0].Text != "summarize this" {
		t.Fatalf("got %+v; want one trimmed entry", got)
	}

	if err := c.SetQuickCommands([]QuickCommandEntry{{Text: "no title"}}); err == nil {
		t.Fatal("title-less entry must be rejected")
	}
	if err := c.SetQuickCommands([]QuickCommandEntry{{Title: "big", Text: string(make([]byte, QuickCommandMaxTextSize+1))}}); err == nil {
		t.Fatal("oversized text must be rejected")
	}
	long := make([]rune, QuickCommandMaxTitle+1)
	for i := range long {
		long[i] = 'x'
	}
	if err := c.SetQuickCommands([]QuickCommandEntry{{Title: string(long), Text: "t"}}); err == nil {
		t.Fatal("overlong title must be rejected")
	}

	// The returned slice must be a copy: mutating it cannot touch config state.
	got[0].Title = "mutated"
	if c.DesktopQuickCommands()[0].Title != "Summary" {
		t.Fatal("DesktopQuickCommands must return a copy")
	}
}
