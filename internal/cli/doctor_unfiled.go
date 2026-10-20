package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"reasonix/internal/agent"
	"reasonix/internal/doctor"
	"reasonix/internal/store"
	"reasonix/internal/unfiled"
)

// doctorSessionUnfiled prints the unfiled-input reconciliation for one session
// (task 1023 phase 0): prompts recorded in the checkpoint sidecar that never
// appeared as a user message in the transcript. Strictly read-only — the
// checkpoint files are parsed directly (never through checkpoint.New, whose
// constructor GCs payloads) and the transcript is replayed through the same
// event-log-aware loader every other reader uses.
func doctorSessionUnfiled(ref string, asJSON bool) int {
	sessionPath, err := doctor.ResolveSessionRef(ref)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	inputs, err := unfiled.ReadCheckpointInputs(store.SessionCheckpointDir(sessionPath))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	users, err := agent.LoadSessionUserMessages(sessionPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	texts := make([]string, 0, len(users))
	for _, u := range users {
		texts = append(texts, u.Message.Content)
	}
	entries := unfiled.Reconcile(inputs, texts)

	type wireEntry struct {
		Turn     int    `json:"turn"`
		MsgIndex int    `json:"msgIndex"`
		Time     string `json:"time"`
		Prompt   string `json:"prompt"`
	}
	out := struct {
		Session     string      `json:"session"`
		Checkpoints int         `json:"checkpoints"`
		Unfiled     int         `json:"unfiled"`
		Entries     []wireEntry `json:"entries"`
	}{
		Session:     sessionPath,
		Checkpoints: len(inputs),
		Unfiled:     len(entries),
		Entries:     make([]wireEntry, 0, len(entries)),
	}
	for _, e := range entries {
		out.Entries = append(out.Entries, wireEntry{
			Turn:     e.Turn,
			MsgIndex: e.MsgIndex,
			Time:     e.Time.Format("2006-01-02T15:04:05-07:00"),
			Prompt:   e.Prompt,
		})
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	fmt.Printf("unfiled-input reconciliation: %s\n", sessionPath)
	fmt.Printf("  checkpoints scanned: %d\n", out.Checkpoints)
	fmt.Printf("  unfiled inputs: %d\n", out.Unfiled)
	for _, e := range entries {
		fmt.Printf("  - %s\n", unfiled.FormatEntry(e))
	}
	if out.Unfiled > 0 {
		fmt.Println("  full text is available via --json; the desktop transcript surfaces the same entries")
	}
	return 0
}
