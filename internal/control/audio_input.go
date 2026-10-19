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

// Task 1008 (模型音频输入): file audio attachments ride the same @-reference
// chain as images. The capability gate decides the attachment entry on the
// frontend and whether the resolved data URLs reach the provider request;
// neither the gate nor the note text ever touches tools or the system prompt,
// so request prefix bytes stay stable across audio capability states.

func (c *Controller) audioInputEnabled() bool {
	if audio := c.activeAudioInput(); audio != nil {
		return *audio
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
		return c.modelCapabilityResolver(entry).AudioInputEnabled
	}
	return c.audioInputEnabledViaConfig()
}

// audioInputEnabledViaConfig is the fallback path without a controller-owned
// resolver (same split as imageInputEnabled / EffectiveVision).
func (c *Controller) audioInputEnabledViaConfig() bool {
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
	return config.EffectiveAudioInput(entry)
}

// AudioInputEnabled reports whether the current model accepts direct audio
// input, so frontends can gate the audio attachment entry before a turn.
func (c *Controller) AudioInputEnabled() bool { return c.audioInputEnabled() }

// resolveTurnAudio resolves each audio @-reference once, only when the active
// model is audio-enabled. A gate-off turn resolves nothing: the refs stay in
// the text as attachment notes, and the provider request keeps its exact
// audio-free byte shape.
func (c *Controller) resolveTurnAudio(line string) []string {
	if !c.audioInputEnabled() {
		return nil
	}
	return c.resolveInputAudioCandidates(line)
}

// resolveInputAudioCandidates resolves audio references (attachment-dir refs
// and workspace files with an accepted audio extension) to inline data URLs.
func (c *Controller) resolveInputAudioCandidates(line string) []string {
	var urls []string
	seen := map[string]bool{}
	for _, r := range c.detectRefs(line) {
		if !isAudioRef(r) {
			continue
		}
		baseDir := c.workspaceRoot
		if r.baseDir != "" {
			baseDir = r.baseDir
		}
		url, err := audioRefValue(r.path, baseDir)
		if err != nil || url == "" || seen[url] {
			continue
		}
		seen[url] = true
		urls = append(urls, url)
	}
	return urls
}

func isAudioRef(r ref) bool {
	switch r.kind {
	case refAudio:
		return true
	case refFile:
		return provider.IsAudioAttachmentPath(r.path)
	default:
		return false
	}
}

// audioRefValue reads one audio reference into a data URL. Unlike images there
// is no vision-compression or Files-API upload step: the bytes ride inline as
// base64, capped at the provider's 50 MB attachment ceiling.
func audioRefValue(pathName, baseDir string) (string, error) {
	if isAttachmentRef(filepath.ToSlash(pathName)) {
		return audioAttachmentDataURL(baseDir, pathName)
	}
	return audioFileDataURL(pathName, baseDir)
}

func audioAttachmentDataURL(base, path string) (string, error) {
	clean, err := cleanAttachmentPath(base, path)
	if err != nil {
		return "", err
	}
	f, err := os.Open(clean)
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := readAudioAttachmentFile(f)
	if err != nil {
		return "", err
	}
	mime := provider.AudioMIMEForPath(path)
	if mime == "" {
		return "", fmt.Errorf("attachment is not an accepted audio format")
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func audioFileDataURL(pathName, baseDir string) (string, error) {
	absPath, absBase, ok := resolveAbsRef(pathName, baseDir)
	if !ok {
		return "", os.ErrNotExist
	}
	if absBase == "" {
		return "", fmt.Errorf("workspace root is required for file audio references")
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
		return "", fmt.Errorf("audio path must not be a symlink")
	}
	if info.IsDir() {
		return "", fmt.Errorf("audio reference must be a file")
	}
	f, err := root.Open(rel)
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := readAudioAttachmentFile(f)
	if err != nil {
		return "", err
	}
	mime := provider.AudioMIMEForPath(pathName)
	if mime == "" {
		return "", fmt.Errorf("audio reference is not an accepted audio format")
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func readAudioAttachmentFile(f *os.File) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() <= 0 || info.Size() > provider.MaxAudioAttachmentBytes {
		return nil, fmt.Errorf("audio attachment must be between 1 byte and 50 MB")
	}
	raw, err := io.ReadAll(io.LimitReader(f, provider.MaxAudioAttachmentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > provider.MaxAudioAttachmentBytes {
		return nil, fmt.Errorf("audio attachment must be between 1 byte and 50 MB")
	}
	return raw, nil
}

// audioAttachmentNote is the audio counterpart of imageAttachmentNote. The
// note lands in the user-turn context block only, so an audio-disabled model
// still learns where the file lives without the request gaining audio bytes.
func audioAttachmentNote(path string, attached bool) string {
	if attached {
		return "[The audio file at @" + path + " is attached as audio input. Listen to the attached audio directly; do not read the file from disk unless the user asks.]"
	}
	return "[audio attachment available at @" + path + "; sent as direct model audio input only when the selected model supports audio input and it is enabled in the model's capability settings. Audio bytes are not inlined into prompt text.]"
}

// audioAttachmentFailedNote is the honest counterpart of audioAttachmentNote,
// mirroring imageAttachmentFailedNote: a reference whose bytes never made it
// into the turn must not be announced as attached.
func audioAttachmentFailedNote(path string, err error) string {
	return "[audio attachment at @" + path + " could not be attached as audio input: " + err.Error() + ". The audio bytes are not in this turn. Tell the user the attachment did not come through; do not guess at the audio content.]"
}

// audioFileRefNote covers workspace (non-attachment) audio file references in
// the resolved-context block; same contract as imageFileRefNote.
func audioFileRefNote(displayPath string, attached bool) string {
	if attached {
		return "[audio file " + displayPath + " — attached as audio input. Listen to the attached audio directly; do not read the file from disk unless the user asks.]"
	}
	return "[audio file " + displayPath + " — sent as direct model audio input only when the selected model supports audio input and it is enabled in the model's capability settings. Audio bytes are not inlined into prompt text.]"
}

// audioRefAvailable reports whether an audio reference can actually be
// delivered this turn, without materialising the payload (mirrors
// visionRefAvailable; note wording must not claim a failed attach).
func (c *Controller) audioRefAvailable(r ref, baseDir string) error {
	switch r.kind {
	case refAudio:
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
		if !provider.IsAudioAttachmentPath(r.path) {
			return fmt.Errorf("reference is not an audio file")
		}
		absPath, _, ok := resolveAbsRef(r.path, baseDir)
		if !ok {
			return os.ErrNotExist
		}
		_, err := os.Stat(absPath)
		return err
	default:
		return fmt.Errorf("reference is not an audio file")
	}
}
