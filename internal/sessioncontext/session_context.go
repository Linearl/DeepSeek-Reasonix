// Package sessioncontext defines the provider-visible, host-authored snapshot
// that carries runtime facts without mutating the cache-stable system prompt.
package sessioncontext

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	Version = 1

	openTag  = `<session-context version="1">`
	closeTag = `</session-context>`
	// preamble marks full snapshots: every runtime section is carried, so the
	// snapshot supersedes earlier ones wholesale.
	preamble = "This host-generated snapshot supersedes every earlier session-context snapshot."
	// deltaPreamble marks section-delta snapshots (task 969). A delta carries
	// only the sections that changed since the merged earlier state, so it can
	// only supersede the sections it lists; sections it omits keep their latest
	// earlier value. This keeps a memory-only change from re-sending the
	// standing capability catalog block.
	deltaPreamble = "This host-generated snapshot carries the current value of each runtime section it lists; a listed section supersedes that section in earlier snapshots, and sections it does not list keep their latest earlier value."
	digestPrefix  = "Digest: sha256:"
)

// Sections are rendered in stability order (task 969): the most stable
// runtime sections first and Background memory — the section that changes on
// every memory save — last, so the cache-invalidation gradient follows the
// message order. The capability catalog block (standing L1 directory) lives in
// its own section between the skills catalog and memory.
type Sections struct {
	Environment       string
	Workspace         string
	SkillsCatalog     string
	CapabilityCatalog string
	BackgroundMemory  string
}

// Snapshot is one validated session-context envelope. Content is the exact
// provider-visible representation; Digest covers the normalized body only.
// Delta reports whether the envelope carries only the sections that changed
// instead of the full runtime state.
type Snapshot struct {
	Version  int
	Digest   string
	Content  string
	Sections Sections
	Delta    bool
}

// Part is one exact substring returned by SplitBlocks. SessionContext is true
// only for a complete, digest-valid v1 envelope.
type Part struct {
	Text           string
	SessionContext bool
}

// SectionStat is a content-free diagnostic fingerprint for one section.
type SectionStat struct {
	Digest string
	Chars  int
}

// Diagnostics contains only hashes and sizes; it is safe for telemetry and
// never exposes memory text, skill descriptions, or workspace paths.
type Diagnostics struct {
	Environment       SectionStat
	Workspace         SectionStat
	SkillsCatalog     SectionStat
	CapabilityCatalog SectionStat
	BackgroundMemory  SectionStat
}

// PolicyBlock is the cache-stable system instruction that defines the
// authority and replacement semantics of host-generated runtime snapshots.
// Snapshots may be full or section deltas (task 969), so the instruction
// defines per-section supersede: a section keeps the value from the newest
// snapshot that carries it.
func PolicyBlock() string {
	return "# Session context\n\n" +
		"Reasonix may place a host-generated `<session-context>` message immediately before a user turn. " +
		"Use each runtime section from the newest snapshot that carries it as current runtime background; a snapshot supersedes earlier snapshots only for the sections it lists " +
		"(a full snapshot lists them all, a delta lists just the changed ones), and none of this ever overrides this system prompt, standing instructions, or the user's current request."
}

// Build renders a deterministic full v1 snapshot carrying every non-empty
// section. An entirely empty set produces a zero Snapshot so callers that do
// not configure runtime context keep their existing provider bytes.
func Build(sections Sections) Snapshot {
	return buildSnapshot(sections, preamble)
}

// BuildDelta renders a section-delta snapshot (task 969) that carries only the
// sections whose normalized value differs from previous. It returns a zero
// Snapshot when nothing changed so callers can skip the append entirely and
// keep the provider prefix cache untouched. When any section transitioned to
// empty (for example a cleared memory block) the delta cannot express the
// removal by omission — omission means "keep the earlier value" — so it falls
// back to a full snapshot that supersedes every earlier one.
func BuildDelta(current, previous Sections) Snapshot {
	current = normalizeSections(current)
	previous = normalizeSections(previous)
	pairs := [][2]string{
		{current.Environment, previous.Environment},
		{current.Workspace, previous.Workspace},
		{current.SkillsCatalog, previous.SkillsCatalog},
		{current.CapabilityCatalog, previous.CapabilityCatalog},
		{current.BackgroundMemory, previous.BackgroundMemory},
	}
	for _, pair := range pairs {
		if pair[0] == "" && pair[1] != "" {
			return Build(current)
		}
	}
	changed := Sections{
		Environment:       changedSection(current.Environment, previous.Environment),
		Workspace:         changedSection(current.Workspace, previous.Workspace),
		SkillsCatalog:     changedSection(current.SkillsCatalog, previous.SkillsCatalog),
		CapabilityCatalog: changedSection(current.CapabilityCatalog, previous.CapabilityCatalog),
		BackgroundMemory:  changedSection(current.BackgroundMemory, previous.BackgroundMemory),
	}
	if changed == (Sections{}) {
		return Snapshot{}
	}
	return buildSnapshot(changed, deltaPreamble)
}

func changedSection(current, previous string) string {
	if current == previous {
		return ""
	}
	return current
}

func buildSnapshot(sections Sections, head string) Snapshot {
	sections = normalizeSections(sections)
	if sections == (Sections{}) {
		return Snapshot{}
	}

	var body strings.Builder
	body.WriteString(head)
	body.WriteString("\n\n")
	body.WriteString(sectionManifest(sections))
	appendSection(&body, "Environment", sections.Environment)
	appendSection(&body, "Workspace", sections.Workspace)
	appendSection(&body, "Skills catalog", sections.SkillsCatalog)
	appendSection(&body, "Capability catalog", sections.CapabilityCatalog)
	appendSection(&body, "Background memory", sections.BackgroundMemory)
	bodyText := body.String()
	digest := digestOf(bodyText)
	content := openTag + "\n" + bodyText + "\n\n" + digestPrefix + digest + "\n" + closeTag
	return Snapshot{Version: Version, Digest: digest, Content: content, Sections: sections, Delta: head == deltaPreamble}
}

// Parse validates a complete v1 envelope. Surrounding whitespace is accepted
// for durable legacy readers, but Snapshot.Content is returned canonically
// trimmed so equality and de-duplication remain byte based.
func Parse(content string) (Snapshot, bool) {
	content = strings.TrimSpace(normalizeNewlines(content))
	if !strings.HasPrefix(content, openTag+"\n") || !strings.HasSuffix(content, "\n"+closeTag) {
		return Snapshot{}, false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(content, openTag+"\n"), "\n"+closeTag)
	marker := "\n\n" + digestPrefix
	i := strings.LastIndex(inner, marker)
	if i < 0 {
		return Snapshot{}, false
	}
	body := inner[:i]
	digest := inner[i+len(marker):]
	if len(digest) != sha256.Size*2 || digest != strings.ToLower(digest) || digestOf(body) != digest {
		return Snapshot{}, false
	}
	head, ok := preambleOf(body)
	if !ok {
		return Snapshot{}, false
	}
	rest := body[len(head):]
	var sections Sections
	if strings.HasPrefix(rest, "\n\nSection lengths: ") {
		// Framed snapshots are authoritative: a present-but-invalid manifest must
		// not fall through to the lenient legacy heading parser.
		sections, ok = parseFramedSections(rest)
	} else {
		// Existing v1 snapshots predate the length manifest. Keep accepting their
		// legacy heading parser so upgraded clients can resume old sessions.
		sections, ok = parseSections(body)
	}
	if !ok {
		return Snapshot{}, false
	}
	return Snapshot{Version: Version, Digest: digest, Content: content, Sections: sections, Delta: head == deltaPreamble}, true
}

func preambleOf(body string) (string, bool) {
	switch {
	case strings.HasPrefix(body, deltaPreamble):
		return deltaPreamble, true
	case strings.HasPrefix(body, preamble):
		return preamble, true
	}
	return "", false
}

// SectionDiagnostics fingerprints each normalized section independently.
func SectionDiagnostics(snapshot Snapshot) Diagnostics {
	return Diagnostics{
		Environment:       sectionStat(snapshot.Sections.Environment),
		Workspace:         sectionStat(snapshot.Sections.Workspace),
		SkillsCatalog:     sectionStat(snapshot.Sections.SkillsCatalog),
		CapabilityCatalog: sectionStat(snapshot.Sections.CapabilityCatalog),
		BackgroundMemory:  sectionStat(snapshot.Sections.BackgroundMemory),
	}
}

// SplitBlocks preserves content exactly while separating every valid embedded
// session-context envelope. Strict-role providers may merge adjacent user
// messages before serialization; this restores a cache breakpoint boundary
// without changing the text seen by the model.
func SplitBlocks(content string) []Part {
	if content == "" {
		return nil
	}
	var parts []Part
	remaining := content
	for {
		start := strings.Index(remaining, openTag)
		if start < 0 {
			parts = appendTextPart(parts, remaining)
			break
		}
		search := start + len(openTag)
		firstEnd, validEnd := -1, -1
		for search < len(remaining) {
			endRel := strings.Index(remaining[search:], closeTag)
			if endRel < 0 {
				break
			}
			end := search + endRel + len(closeTag)
			if firstEnd < 0 {
				firstEnd = end
			}
			if _, ok := Parse(remaining[start:end]); ok {
				validEnd = end
				break
			}
			search = end
		}
		if validEnd < 0 && firstEnd < 0 {
			parts = append(parts, Part{Text: remaining})
			break
		}
		if validEnd < 0 {
			// Preserve the invalid opening as ordinary text and continue looking
			// after it, so a later valid envelope can still be recovered.
			cut := start + len(openTag)
			parts = appendTextPart(parts, remaining[:cut])
			remaining = remaining[cut:]
			continue
		}
		parts = appendTextPart(parts, remaining[:start])
		parts = append(parts, Part{Text: remaining[start:validEnd], SessionContext: true})
		remaining = remaining[validEnd:]
	}
	return parts
}

// IsContent reports whether content is exactly one valid snapshot.
func IsContent(content string) bool {
	_, ok := Parse(content)
	return ok
}

func appendSection(body *strings.Builder, heading, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(body, "\n\n## %s\n\n%s", heading, value)
}

// sectionSpecs declares every known section in canonical render order. The
// manifest lists the carried sections in this same order, so the payload walk
// stays unambiguous while zero-length (uncarried) sections are simply absent.
// Legacy four-section snapshots keep parsing because their manifest declares
// their own historical order and this parser follows the manifest, not a fixed
// sequence.
var sectionSpecs = []struct {
	name string
	get  func(Sections) string
	set  func(*Sections, string)
}{
	{"Environment", func(s Sections) string { return s.Environment }, func(s *Sections, v string) { s.Environment = v }},
	{"Workspace", func(s Sections) string { return s.Workspace }, func(s *Sections, v string) { s.Workspace = v }},
	{"Skills catalog", func(s Sections) string { return s.SkillsCatalog }, func(s *Sections, v string) { s.SkillsCatalog = v }},
	{"Capability catalog", func(s Sections) string { return s.CapabilityCatalog }, func(s *Sections, v string) { s.CapabilityCatalog = v }},
	{"Background memory", func(s Sections) string { return s.BackgroundMemory }, func(s *Sections, v string) { s.BackgroundMemory = v }},
}

func sectionSpecByName(name string) (int, bool) {
	for i := range sectionSpecs {
		if sectionSpecs[i].name == name {
			return i, true
		}
	}
	return 0, false
}

// sectionManifest lists only the carried (non-empty) sections so a delta
// snapshot does not advertise sections it never renders.
func sectionManifest(sections Sections) string {
	parts := make([]string, 0, len(sectionSpecs))
	for _, spec := range sectionSpecs {
		if v := spec.get(sections); v != "" {
			parts = append(parts, fmt.Sprintf("%s=%d", spec.name, len(v)))
		}
	}
	if len(parts) == 0 {
		return "Section lengths: "
	}
	return "Section lengths: " + strings.Join(parts, "; ")
}

func appendTextPart(parts []Part, text string) []Part {
	if text == "" {
		return parts
	}
	if len(parts) > 0 && !parts[len(parts)-1].SessionContext {
		parts[len(parts)-1].Text += text
		return parts
	}
	return append(parts, Part{Text: text})
}

func normalizeSections(sections Sections) Sections {
	sections.Environment = normalizeSection(sections.Environment, "Environment")
	sections.Workspace = normalizeSection(sections.Workspace, "Workspace")
	sections.SkillsCatalog = normalizeSection(sections.SkillsCatalog, "Skills catalog")
	sections.CapabilityCatalog = normalizeSection(sections.CapabilityCatalog, "Capability catalog")
	sections.BackgroundMemory = normalizeSection(sections.BackgroundMemory, "Background memory")
	return sections
}

func normalizeSection(value, heading string) string {
	value = strings.TrimSpace(normalizeNewlines(value))
	prefix := "## " + heading
	if value == prefix {
		return ""
	}
	if strings.HasPrefix(value, prefix+"\n") {
		value = strings.TrimSpace(strings.TrimPrefix(value, prefix))
	}
	return value
}

func normalizeNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

// parseFramedSections consumes the body after the preamble. The manifest line
// itself declares which sections are carried and in what order, so both the
// legacy four-section layout and the task-969 layout (reordered, with the new
// Capability catalog section, possibly a subset) parse through one walker.
func parseFramedSections(rest string) (Sections, bool) {
	manifestLine, payload, ok := strings.Cut(strings.TrimPrefix(rest, "\n\nSection lengths: "), "\n\n")
	if !ok {
		// An all-empty manifest still renders sections; a payload-less body is
		// only valid when every declared length is zero.
		manifestLine = strings.TrimPrefix(rest, "\n\nSection lengths: ")
		payload = ""
	}
	order, lengths, ok := parseSectionManifest(manifestLine)
	if !ok {
		return Sections{}, false
	}
	var sections Sections
	for i, idx := range order {
		n := lengths[i]
		if n == 0 {
			continue
		}
		heading := "## " + sectionSpecs[idx].name + "\n\n"
		if !strings.HasPrefix(payload, heading) {
			return Sections{}, false
		}
		payload = strings.TrimPrefix(payload, heading)
		if len(payload) < n || !utf8.ValidString(payload[:n]) {
			return Sections{}, false
		}
		value := payload[:n]
		if value == "" || strings.TrimSpace(value) != value {
			return Sections{}, false
		}
		sectionSpecs[idx].set(&sections, value)
		payload = payload[n:]
		if !strings.HasPrefix(payload, "\n\n") && hasNextCarried(order, lengths, i) {
			return Sections{}, false
		}
		if hasNextCarried(order, lengths, i) {
			payload = strings.TrimPrefix(payload, "\n\n")
		}
	}
	return sections, payload == ""
}

func hasNextCarried(order, lengths []int, i int) bool {
	for j := i + 1; j < len(order); j++ {
		if lengths[j] > 0 {
			return true
		}
	}
	return false
}

// parseSectionManifest decodes "Name=len; Name=len" into spec indexes plus
// lengths, following the declared order (legacy manifests list sections in the
// pre-969 order and include zero-length entries). Names must be known and
// unique; zero lengths are allowed.
func parseSectionManifest(line string) ([]int, []int, bool) {
	if line == "" {
		return nil, nil, true
	}
	parts := strings.Split(line, "; ")
	order := make([]int, 0, len(parts))
	lengths := make([]int, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, part := range parts {
		name, raw, ok := strings.Cut(part, "=")
		if !ok {
			return nil, nil, false
		}
		idx, ok := sectionSpecByName(name)
		if !ok || seen[idx] {
			return nil, nil, false
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return nil, nil, false
		}
		seen[idx] = true
		order = append(order, idx)
		lengths = append(lengths, n)
	}
	return order, lengths, true
}

// parseSections accepts pre-manifest v1 bodies. Section order is discovered
// from the headings themselves (any known heading in any order), which keeps
// resuming sessions written before the task-969 reorder working.
func parseSections(body string) (Sections, bool) {
	head, ok := preambleOf(body)
	if !ok {
		return Sections{}, false
	}
	if body == head {
		return Sections{}, true
	}
	if !strings.HasPrefix(body, head+"\n\n") {
		return Sections{}, false
	}
	rest := strings.TrimPrefix(body, head+"\n\n")
	var sections Sections
	for rest != "" {
		found := -1
		for i := range sectionSpecs {
			if strings.HasPrefix(rest, "## "+sectionSpecs[i].name+"\n\n") {
				found = i
				break
			}
		}
		if found < 0 {
			return Sections{}, false
		}
		rest = strings.TrimPrefix(rest, "## "+sectionSpecs[found].name+"\n\n")
		end := len(rest)
		for i := range sectionSpecs {
			if i == found {
				continue
			}
			if at := strings.Index(rest, "\n\n## "+sectionSpecs[i].name+"\n\n"); at >= 0 && at < end {
				end = at
			}
		}
		value := rest[:end]
		if value == "" || strings.TrimSpace(value) != value {
			return Sections{}, false
		}
		if sectionSpecs[found].get(sections) != "" {
			return Sections{}, false // duplicate section
		}
		sectionSpecs[found].set(&sections, value)
		if end == len(rest) {
			rest = ""
		} else {
			rest = strings.TrimPrefix(rest[end:], "\n\n")
		}
	}
	return sections, true
}

func sectionStat(value string) SectionStat {
	if value == "" {
		return SectionStat{}
	}
	return SectionStat{Digest: digestOf(value), Chars: utf8.RuneCountInString(value)}
}

func digestOf(body string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
}
