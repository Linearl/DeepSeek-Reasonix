package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"reasonix/internal/localopen"
	"reasonix/internal/tool"
)

// render_artifact (任务 944) declares "this file is a deliverable for the
// user" so the transcript can render an artifact card instead of a plain
// tool row. The tool itself is display-only: it validates the target with
// the SAME gate as the opener family (internal/localopen —拒绝可执行目标、
// symlink 逃逸、非法路径语法), resolves the card kind, and returns a JSON
// payload the frontend ArtifactCard parses. Every open action on the card
// re-enters the desktop gate (OpenLocalPath / OpenLocalPathInExternalOpener),
// so a stale card cannot bypass the opener's checks.
//
// Registered only under the experimental_render_artifact lab switch (task 944
// 铁律 2: off = tool absent, zero behaviour). Idempotent merge (同一 path 重复
// 调用只留最新一张卡) is a frontend render-layer dedup, keeping this tool
// stateless.

// renderArtifactPreviewMaxLines bounds the md/text preview embedded in the
// tool result (sketch §3.2: 行数上限防大文件卡顿).
const renderArtifactPreviewMaxLines = 40

// renderArtifactPreviewMaxBytes bounds the preview byte-wise so a minified
// one-line file cannot flood the card or the model context.
const renderArtifactPreviewMaxBytes = 8 << 10

type renderArtifactTool struct {
	// workDir resolves relative paths (absolute paths pass through). Empty
	// means relative paths are rejected — the tool still works for absolute
	// targets, which is what model output usually provides.
	workDir string
}

// NewRenderArtifactTool constructs the task-944 artifact-card tool. workDir
// is the boot workspace root used to resolve workspace-relative paths.
func NewRenderArtifactTool(workDir string) tool.Tool {
	return renderArtifactTool{workDir: strings.TrimSpace(workDir)}
}

func (renderArtifactTool) Name() string { return "render_artifact" }

func (renderArtifactTool) Description() string {
	return "Render a deliverable file (report, document, output) as an artifact card in the user's chat. " +
		"The card shows a title, a markdown/text preview and open actions (default app, choose app, reveal in file manager). " +
		"Use it once per deliverable after writing the file; calling it again with the same path refreshes the existing card. " +
		"Paths must point to an existing regular file — executables are refused."
}

func (renderArtifactTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Absolute path, or a path relative to the workspace root, of the deliverable file."
    },
    "title": {
      "type": "string",
      "description": "Card title. Defaults to the file name."
    },
    "kind": {
      "type": "string",
      "enum": ["auto", "md", "html", "pdf", "image", "text"],
      "description": "Card kind. auto (default) infers from the file extension: .md/.markdown/.mdx → md, .html/.htm → html, .pdf → pdf, common image extensions → image, everything else → text. md/text cards embed a bounded preview; html/pdf/image cards are open-only."
    },
    "note": {
      "type": "string",
      "description": "Optional one-line note shown on the card (e.g. what the deliverable contains)."
    }
  },
  "required": ["path"]
}`)
}

// ReadOnly is true: the tool touches no host state — it validates a path and
// returns display metadata, so parallel batching is safe.
func (renderArtifactTool) ReadOnly() bool { return true }

// renderArtifactArgs is one validated render_artifact call.
type renderArtifactArgs struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	Note  string `json:"note"`
}

// renderArtifactResult is the JSON payload returned to the model AND stored
// as the tool result; the frontend ArtifactCard parses it (live via the tool
// event, history via ToolResultForTab).
type renderArtifactResult struct {
	OK               bool   `json:"ok"`
	Path             string `json:"path"`
	Title            string `json:"title"`
	Kind             string `json:"kind"`
	Note             string `json:"note,omitempty"`
	Size             int64  `json:"size"`
	Preview          string `json:"preview,omitempty"`
	PreviewTruncated bool   `json:"previewTruncated,omitempty"`
}

// artifactKindForPath maps a file extension to a card kind. Unknown
// extensions fall back to "text" (monospace preview, open works) — the safe
// default that never embeds rich rendering for an untrusted format.
func artifactKindForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".mdx":
		return "md"
	case ".html", ".htm":
		return "html"
	case ".pdf":
		return "pdf"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".ico", ".avif":
		return "image"
	default:
		return "text"
	}
}

func normalizeArtifactKind(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "auto":
		return "auto", nil
	case "md", "html", "pdf", "image", "text":
		return strings.ToLower(strings.TrimSpace(kind)), nil
	default:
		return "", fmt.Errorf("unknown kind %q (want auto|md|html|pdf|image|text)", kind)
	}
}

func (r renderArtifactTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var args renderArtifactArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid render_artifact arguments: %w", err)
	}
	args.Path = strings.TrimSpace(args.Path)
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	wantedKind, err := normalizeArtifactKind(args.Kind)
	if err != nil {
		return "", err
	}

	// Resolve workspace-relative paths, then run the SHARED opener gate
	// (internal/localopen) — same normalization, same executable-target
	// refusal as a markdown-link click.
	path := args.Path
	if !filepath.IsAbs(path) {
		if r.workDir == "" {
			return "", fmt.Errorf("path %q is not absolute and no workspace root is available", args.Path)
		}
		path = filepath.Join(r.workDir, path)
	}
	path, err = localopen.Normalize(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("artifact file does not exist: %q", path)
		}
		return "", fmt.Errorf("artifact file unavailable: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("artifact target is a directory, not a file: %q", path)
	}
	if !localopen.TargetPathAllowed(path, info) {
		return "", fmt.Errorf("refusing to card executable target %q", path)
	}

	kind := wantedKind
	if kind == "auto" {
		kind = artifactKindForPath(path)
	}
	title := strings.TrimSpace(args.Title)
	if title == "" {
		title = filepath.Base(path)
	}

	result := renderArtifactResult{
		OK:    true,
		Path:  path,
		Title: title,
		Kind:  kind,
		Note:  strings.TrimSpace(args.Note),
		Size:  info.Size(),
	}
	// md/text cards embed a bounded preview; html/pdf/image are open-only
	// (首期不做内嵌沙箱预览, sketch §3.4 风险 1).
	if kind == "md" || kind == "text" {
		preview, truncated, err := readArtifactPreview(path)
		if err != nil {
			// A preview failure never blocks the card: drop the preview and
			// keep the header/actions alive.
			preview, truncated = "", false
		}
		result.Preview = preview
		result.PreviewTruncated = truncated
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode artifact result: %w", err)
	}
	return string(encoded), nil
}

// readArtifactPreview reads the first renderArtifactPreviewMaxLines lines /
// renderArtifactPreviewMaxBytes of a text-ish file. Binary content (NUL bytes
// or invalid UTF-8) yields no preview — the card degrades to header+actions.
func readArtifactPreview(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	truncated := false
	if len(data) > renderArtifactPreviewMaxBytes {
		data = data[:renderArtifactPreviewMaxBytes]
		truncated = true
	}
	if strings.ContainsRune(string(data), '\x00') || !utf8.Valid(data) {
		return "", false, nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	if len(lines) > renderArtifactPreviewMaxLines {
		lines = lines[:renderArtifactPreviewMaxLines]
		truncated = true
	}
	return strings.TrimRight(strings.Join(lines, ""), "\n"), truncated, nil
}
