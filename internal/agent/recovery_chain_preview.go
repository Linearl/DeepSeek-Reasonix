package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"reasonix/internal/provider"
)

// RecoveryChainPreview is what the merge picker shows in a dialog when the user
// clicks a candidate chain: enough of the branch's content to judge it against
// the other chains - its size, how it starts, how it ends, and what picking it
// would keep or drop relative to the current main. It is computed in memory
// from the transcript files; nothing is written.
type RecoveryChainPreview struct {
	Path           string   `json:"path"`
	IsMain         bool     `json:"isMain"`
	MessageCount   int      `json:"messageCount"`
	Turns          int      `json:"turns"`
	FirstUserText  string   `json:"firstUserText"`
	TailLines      []string `json:"tailLines"`
	SharedWithMain int      `json:"sharedWithMain"`
	UniqueToChain  int      `json:"uniqueToChain"`
	LastActivity   string   `json:"lastActivity"`
}

// previewLine caps one preview line so a pathological single message cannot
// blow up the dialog.
func previewLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return s
}

// RecoveryChainPreviewFor reads chainPath (the main itself or one of its
// recovery copies) and builds the preview. Reading is passive: the chain files
// are only ever opened, never rewritten, so a preview can be repeated freely
// and cancels cleanly.
func RecoveryChainPreviewFor(mainPath, chainPath string) (RecoveryChainPreview, error) {
	mainPath = filepath.Clean(strings.TrimSpace(mainPath))
	chainPath = filepath.Clean(strings.TrimSpace(chainPath))
	if mainPath == "" || chainPath == "" {
		return RecoveryChainPreview{}, fmt.Errorf("a transcript path is required for a chain preview")
	}

	// The chain must belong to this session: either the main itself or one of
	// the recovery copies the enumerator finds. Letting an arbitrary path
	// through would turn the preview into an unrestricted transcript reader.
	allowed := chainPath == mainPath
	if !allowed {
		copies, err := recoveryCopiesForMain(mainPath)
		if err != nil {
			return RecoveryChainPreview{}, err
		}
		for _, copy := range copies {
			if copy == chainPath {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return RecoveryChainPreview{}, fmt.Errorf("the requested chain is not part of this session: %s", chainPath)
	}

	snap, ok := LoadSessionContentSnapshot(chainPath)
	if !ok {
		return RecoveryChainPreview{}, fmt.Errorf("the branch could not be loaded for preview: %s", chainPath)
	}

	preview := RecoveryChainPreview{
		Path:         chainPath,
		IsMain:       chainPath == mainPath,
		MessageCount: snap.Len(),
	}
	turns := 0
	for _, msg := range snap.messages {
		if IsUserAuthoredTurnMessage(msg) {
			turns++
			if preview.FirstUserText == "" {
				preview.FirstUserText = previewLine(UserMessageText(msg), 160)
			}
		}
	}
	// The tail is taken from the end: what the branch would add at the bottom
	// of the merged conversation is what the user judges it by.
	tailKeep := 3
	msgs := snap.messages
	if n := len(msgs); n > 0 {
		start := n - tailKeep
		if start < 0 {
			start = 0
		}
		for _, msg := range msgs[start:] {
			text := previewLine(MessageTextForPreview(msg), 160)
			if text == "" {
				continue
			}
			preview.TailLines = append(preview.TailLines, text)
		}
	}
	preview.Turns = turns

	if chainPath == mainPath {
		preview.SharedWithMain = snap.Len()
	} else if overlap, ok := SessionContentOverlap(mainPath, chainPath); ok {
		preview.SharedWithMain = overlap.Shared
		preview.UniqueToChain = overlap.Unique
	}
	return preview, nil
}

// MessageTextForPreview flattens one message to the text a human reads. The
// raw content wins over the provider projection: branches often carry payloads
// the provider view has already summarized away.
func MessageTextForPreview(msg provider.Message) string {
	if text := strings.TrimSpace(UserMessageText(msg)); text != "" {
		return text
	}
	if s := strings.TrimSpace(msg.RawContent); s != "" {
		return s
	}
	return strings.TrimSpace(msg.Content)
}
