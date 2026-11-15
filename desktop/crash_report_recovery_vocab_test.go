package main

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"
)

// tl1605: the crash-report ingest enum (workers/crash-report/src/report_schema.ts
// recoverySchema) and the desktop recovery vocabulary
// (webview2_diagnostics.go) are two hand-maintained copies of one vocabulary
// across a module boundary — the desktop Go module cannot import the TS worker
// and vice versa. Three times (tl1589 was the third) the two sides had to be
// reconciled by hand after drift, and every missing value on the ingest side
// is a silent telemetry outage: zod safeParse answers 400 and the report never
// reaches storage. This test reconciles the two sets on every desktop test
// run: any value present on one side only is red, with the diff named.
//
// tl1598 同族手法（词表并集相等断言）；the canonical Go registration point is
// webView2RecoveryRendererValues, so a new outcome must be added there and in
// the schema in the same edit to stay green.
func TestCrashReportRecoveryVocabularyMatchesIngestSchema(t *testing.T) {
	schemaValues, err := parseRecoverySchemaEnum("../workers/crash-report/src/report_schema.ts")
	if err != nil {
		t.Fatalf("parse report_schema.ts recoverySchema: %v", err)
	}
	if len(schemaValues) == 0 {
		t.Fatal("recoverySchema enum parsed empty — parser or schema rot")
	}

	goValues := append([]string{}, webView2RecoveryRendererValues...)
	goValues = append(goValues, webView2RecoveryNotApplicable, webView2RecoveryBrowserRestart)

	if dup := firstDuplicate(goValues); dup != "" {
		t.Fatalf("desktop recovery vocabulary has duplicate %q", dup)
	}
	if dup := firstDuplicate(schemaValues); dup != "" {
		t.Fatalf("recoverySchema has duplicate %q", dup)
	}

	goSet := make(map[string]bool, len(goValues))
	for _, v := range goValues {
		goSet[v] = true
	}
	schemaSet := make(map[string]bool, len(schemaValues))
	for _, v := range schemaValues {
		schemaSet[v] = true
	}

	missingInSchema := []string{}
	missingInGo := []string{}
	for v := range goSet {
		if !schemaSet[v] {
			missingInSchema = append(missingInSchema, v)
		}
	}
	for v := range schemaSet {
		if !goSet[v] {
			missingInGo = append(missingInGo, v)
		}
	}
	if len(missingInSchema) > 0 || len(missingInGo) > 0 {
		t.Fatalf("recovery vocabulary drift — desktop-only (ingest will 400-drop these): %v; schema-only (dead ingest values): %v; add both sides in the same edit (webView2RecoveryRendererValues + report_schema.ts recoverySchema)",
			missingInSchema, missingInGo)
	}

	// The renderer slice is the registration point, not just a list: the
	// normalizer must accept exactly its values verbatim and nothing else,
	// so a hand-wired switch cannot quietly diverge from the reconciled set.
	for _, v := range webView2RecoveryRendererValues {
		if got := normalizeWebView2Recovery(v); got != v {
			t.Fatalf("normalizeWebView2Recovery(%q) = %q, want verbatim", v, got)
		}
	}
	for _, probe := range []string{webView2RecoveryNotApplicable, webView2RecoveryBrowserRestart, "", "reload_unknown", "RELOAD_FAILED"} {
		if got := normalizeWebView2Recovery(probe); got != webView2RecoveryNotApplicable {
			t.Fatalf("normalizeWebView2Recovery(%q) = %q, want %q", probe, got, webView2RecoveryNotApplicable)
		}
	}
}

// parseRecoverySchemaEnum extracts the string members of the
// `const recoverySchema = z.enum([...])` declaration from the TS source. The
// schema file stays the single ingest-side truth; this test reads it instead
// of holding a third copy of the vocabulary.
func parseRecoverySchemaEnum(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	const marker = "const recoverySchema = z.enum(["
	member := regexp.MustCompile(`"([a-z_]+)"`)

	var values []string
	inEnum := false
	done := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !inEnum {
			idx := strings.Index(line, marker)
			if idx < 0 {
				continue
			}
			inEnum = true
			line = line[idx+len(marker):]
		}
		end := strings.Index(line, "]);")
		if end >= 0 {
			line = line[:end]
			done = true
		}
		for _, m := range member.FindAllStringSubmatch(line, -1) {
			values = append(values, m[1])
		}
		if done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !inEnum {
		return nil, os.ErrNotExist
	}
	return values, nil
}

func firstDuplicate(values []string) string {
	seen := make(map[string]bool, len(values))
	for _, v := range values {
		if seen[v] {
			return v
		}
		seen[v] = true
	}
	return ""
}
