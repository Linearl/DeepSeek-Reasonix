package main

import (
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/agent"
)

// tl1330: several surfaces fell back to the raw transcript id when a session
// carried no better name. A transcript id is "<stamp>-<provider>-<model>"
// (internal/agent.NewSessionPath), and a recovery fork appends
// "-recovery-<16hex>" (internal/agent.stableRecoverySessionPath), so the
// fallback read to the user as a leaked model string plus an internal hash —
// the "启动时模型名带一长串" report. The helpers here give every such surface
// one friendly chain, mirroring the tl1076 repair-panel contract
// (sessionEventsDisplayTitle): user-chosen session title, sidecar topic
// title, listing preview (the first user message), a time label carved from
// the file name, and finally the conversation (topic) label the row would
// otherwise wear. Raw transcript ids never pass through.

// sessionDisplayLabelMaxRunes caps the prose candidates. Matches the clamp
// sessionEventsDisplayTitle has used since tl1076, so every fallback label in
// the app truncates at the same width.
const sessionDisplayLabelMaxRunes = 40

// truncateSessionDisplayLabel clamps one prose candidate to the shared width.
func truncateSessionDisplayLabel(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > sessionDisplayLabelMaxRunes {
		return string(runes[:sessionDisplayLabelMaxRunes]) + "…"
	}
	return text
}

// sessionStampLabelFromBase carves "0927 03:00" out of a transcript name that
// follows the "<20060102-1504...>..." stamp convention. Every name
// NewSessionPath mints (and every recovery fork of one — the stem keeps the
// stamp prefix) parses; anything else yields "" and the caller moves on to
// the next candidate. Extracted from sessionEventsDisplayTitle so every
// fallback label shares one implementation.
func sessionStampLabelFromBase(base string) string {
	base = strings.TrimSpace(base)
	if len(base) >= 13 {
		if _, err := time.Parse("20060102-1504", base[:13]); err == nil {
			return base[:8] + " " + base[9:11] + ":" + base[11:13]
		}
	}
	return ""
}

// recoveredSessionLabelSuffix is the distinguishing marker for recovery-named
// transcripts. Two active sessions of one conversation inherit the same topic
// title and the same preview, so without the marker their two sidebar rows
// would collide into identical friendly labels (tl1330 修复方向：
// 「继承原会话标题+『(已恢复)』后缀」).
func (a *App) recoveredSessionLabelSuffix() string {
	switch a.desktopLocale.Load() {
	case desktopLocaleZh:
		return "（已恢复）"
	case desktopLocaleZhTW:
		return "（已恢復）"
	default:
		return " (recovered)"
	}
}

// sessionDisplayLabelForPath renders the user-facing label for one session
// file wherever no better name is already in hand. topicLabel is the
// conversation (topic) title the surrounding row already resolved — the last
// candidate before the localized default. Recovery-named files add the
// localized recovered marker on every candidate so sibling rows of one
// conversation stay tellable apart.
func (a *App) sessionDisplayLabelForPath(sessionPath, topicLabel string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	topicLabel = strings.TrimSpace(topicLabel)
	if sessionPath == "" {
		return topicLabel
	}
	label := ""
	base := filepath.Base(sessionPath)
	if title := strings.TrimSpace(loadSessionTitles(filepath.Dir(sessionPath))[base]); title != "" {
		label = truncateSessionDisplayLabel(agent.UserPreviewText(title))
	} else if meta, ok, err := agent.LoadBranchMeta(sessionPath); err == nil && ok {
		if title := topicTitleFromText(meta.TopicTitle); title != "" {
			label = title
		} else if preview := strings.TrimSpace(meta.Preview); preview != "" {
			label = truncateSessionDisplayLabel(agent.UserPreviewText(preview))
		}
	}
	if label == "" {
		if stamp := sessionStampLabelFromBase(strings.TrimSuffix(base, filepath.Ext(base))); stamp != "" {
			label = stamp
		}
	}
	if label == "" {
		label = topicLabel
	}
	if label == "" {
		label = a.localizedDefaultTopicTitle()
	}
	if agent.LooksLikeRecoveryFilename(sessionPath) {
		label += a.recoveredSessionLabelSuffix()
	}
	return label
}
