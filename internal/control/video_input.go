package control

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"reasonix/internal/config"
	"reasonix/internal/provider"
)

// tl1767 (视频输入接入): file video attachments ride the same @-reference
// chain as images and audio (task 1008). The capability gate decides the
// attachment entry on the frontend and whether the resolved data URLs reach
// the provider request; neither the gate nor the note text ever touches tools
// or the system prompt, so request prefix bytes stay stable across video
// capability states. Unlike audio, a protocol that cannot carry video never
// fails the request — the adapters project the payload to a text placeholder
// (fail-closed, zcode transform.ts:242 口径).

func (c *Controller) videoInputEnabled() bool {
	if video := c.activeVideoInput(); video != nil {
		return *video
	}
	if c.modelCapabilityResolver != nil {
		ref := c.activeModelRef()
		cfg, err := config.LoadForRoot(c.workspaceRoot)
		if err == nil && ref == "" {
			ref = cfg.DefaultModel
		}
		if err != nil || ref == "" {
			return false
		}
		entry, ok := cfg.ResolveModel(ref)
		if !ok {
			return false
		}
		return c.modelCapabilityResolver(entry).VideoInputEnabled
	}
	return c.videoInputEnabledViaConfig()
}

// videoInputEnabledViaConfig is the fallback path without a controller-owned
// resolver (same split as audioInputEnabled / EffectiveAudioInput).
func (c *Controller) videoInputEnabledViaConfig() bool {
	ref := c.activeModelRef()
	cfg, err := config.LoadForRoot(c.workspaceRoot)
	if err == nil && ref == "" {
		ref = cfg.DefaultModel
	}
	if err != nil || ref == "" {
		return false
	}
	entry, ok := cfg.ResolveModel(ref)
	if !ok {
		return false
	}
	return config.EffectiveVideoInput(entry)
}

// VideoInputEnabled reports whether the current model accepts direct video
// input, so frontends can gate the video attachment entry before a turn.
func (c *Controller) VideoInputEnabled() bool { return c.videoInputEnabled() }

// resolveTurnVideo resolves each video @-reference once, only when the active
// model is video-enabled. A gate-off turn resolves nothing: the refs stay in
// the text as attachment notes, and the provider request keeps its exact
// video-free byte shape.
func (c *Controller) resolveTurnVideo(line string) []string {
	if !c.videoInputEnabled() {
		return nil
	}
	return c.resolveInputVideoCandidates(line)
}

// resolveInputVideoCandidates resolves video references (attachment-dir refs
// and workspace files with an accepted video extension) to inline data URLs.
func (c *Controller) resolveInputVideoCandidates(line string) []string {
	var urls []string
	seen := map[string]bool{}
	for _, r := range c.detectRefs(line) {
		if !isVideoRef(r) {
			continue
		}
		baseDir := c.workspaceRoot
		if r.baseDir != "" {
			baseDir = r.baseDir
		}
		url, err := videoRefValue(r.path, baseDir)
		if err != nil || url == "" || seen[url] {
			continue
		}
		seen[url] = true
		urls = append(urls, url)
	}
	return urls
}

func isVideoRef(r ref) bool {
	switch r.kind {
	case refVideo:
		return true
	case refFile:
		return provider.IsVideoAttachmentPath(r.path)
	default:
		return false
	}
}

// videoRefValue reads one video reference into a data URL. Like audio there is
// no compression or Files-API upload step: the bytes ride inline as base64,
// capped at the provider's 50 MB attachment ceiling.
func videoRefValue(pathName, baseDir string) (string, error) {
	if isAttachmentRef(filepath.ToSlash(pathName)) {
		return videoAttachmentDataURL(baseDir, pathName)
	}
	return videoFileDataURL(pathName, baseDir)
}

func videoAttachmentDataURL(base, path string) (string, error) {
	clean, err := cleanAttachmentPath(base, path)
	if err != nil {
		return "", err
	}
	f, err := os.Open(clean)
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := readVideoAttachmentFile(f)
	if err != nil {
		return "", err
	}
	mime := provider.VideoMIMEForPath(path)
	if mime == "" {
		return "", fmt.Errorf("attachment is not an accepted video format")
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func videoFileDataURL(pathName, baseDir string) (string, error) {
	absPath, absBase, ok := resolveAbsRef(pathName, baseDir)
	if !ok {
		return "", os.ErrNotExist
	}
	if absBase == "" {
		return "", fmt.Errorf("workspace root is required for file video references")
	}
	root, err := os.OpenRoot(absBase)
	if err != nil {
		return "", err
	}
	defer root.Close()
	rel, err := filepath.Rel(absBase, absPath)
	if err != nil {
		return "", err
	}
	info, err := root.Lstat(rel)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("video path must not be a symlink")
	}
	if info.IsDir() {
		return "", fmt.Errorf("video reference must be a file")
	}
	f, err := root.Open(rel)
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := readVideoAttachmentFile(f)
	if err != nil {
		return "", err
	}
	mime := provider.VideoMIMEForPath(pathName)
	if mime == "" {
		return "", fmt.Errorf("video reference is not an accepted video format")
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func readVideoAttachmentFile(f *os.File) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() <= 0 || info.Size() > provider.MaxVideoAttachmentBytes {
		return nil, fmt.Errorf("video attachment must be between 1 byte and 50 MB")
	}
	raw, err := io.ReadAll(io.LimitReader(f, provider.MaxVideoAttachmentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > provider.MaxVideoAttachmentBytes {
		return nil, fmt.Errorf("video attachment must be between 1 byte and 50 MB")
	}
	return raw, nil
}

// videoAttachmentNote is the video counterpart of audioAttachmentNote. The
// note lands in the user-turn context block only, so a video-disabled model
// still learns where the file lives without the request gaining video bytes.
func videoAttachmentNote(path string, attached bool) string {
	if attached {
		return "[The video file at @" + path + " is attached as video input. Watch the attached video directly; do not read the file from disk unless the user asks.]"
	}
	return "[video attachment available at @" + path + "; sent as direct model video input only when the selected model supports video input, it is enabled in the model's capability settings, and the provider protocol can carry video. Video bytes are not inlined into prompt text.]"
}

// videoAttachmentFailedNote is the honest counterpart of videoAttachmentNote,
// mirroring audioAttachmentFailedNote: a reference whose bytes never made it
// into the turn must not be announced as attached.
func videoAttachmentFailedNote(path string, err error) string {
	return "[video attachment at @" + path + " could not be attached as video input: " + err.Error() + ". The video bytes are not in this turn. Tell the user the attachment did not come through; do not guess at the video content.]"
}

// videoFileRefNote covers workspace (non-attachment) video file references in
// the resolved-context block; same contract as audioFileRefNote.
func videoFileRefNote(displayPath string, attached bool) string {
	if attached {
		return "[video file " + displayPath + " — attached as video input. Watch the attached video directly; do not read the file from disk unless the user asks.]"
	}
	return "[video file " + displayPath + " — sent as direct model video input only when the selected model supports video input, it is enabled in the model's capability settings, and the provider protocol can carry video. Video bytes are not inlined into prompt text.]"
}

// videoRefAvailable reports whether a video reference can actually be
// delivered this turn, without materialising the payload (mirrors
// audioRefAvailable; note wording must not claim a failed attach).
func (c *Controller) videoRefAvailable(r ref, baseDir string) error {
	switch r.kind {
	case refVideo:
		if isAttachmentRef(filepath.ToSlash(r.path)) {
			_, err := cleanAttachmentPath(baseDir, r.path)
			return err
		}
		absPath, _, ok := resolveAbsRef(r.path, baseDir)
		if !ok {
			return os.ErrNotExist
		}
		_, err := os.Stat(absPath)
		return err
	case refFile:
		if !provider.IsVideoAttachmentPath(r.path) {
			return fmt.Errorf("reference is not a video file")
		}
		absPath, _, ok := resolveAbsRef(r.path, baseDir)
		if !ok {
			return os.ErrNotExist
		}
		_, err := os.Stat(absPath)
		return err
	default:
		return fmt.Errorf("reference is not a video file")
	}
}
