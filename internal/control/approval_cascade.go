package control

import "strings"

// approvalCascadePrompt renders the tool decision as the question the task
// source sees (task 225). It carries the same three facts the local dialog
// shows — subject, tool, reason — so the source's ask-risk classifier judges
// the same text a human would, keeping the risk boundary in one place.
func approvalCascadePrompt(subject, reason string) string {
	var b strings.Builder
	b.WriteString("The delegated session requests approval for: ")
	b.WriteString(strings.TrimSpace(subject))
	if r := strings.TrimSpace(reason); r != "" {
		b.WriteString(" (")
		b.WriteString(r)
		b.WriteString(")")
	}
	return b.String()
}
