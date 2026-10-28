package main

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// tl1228 阶段一回归：desktop 两处内容布尔（resolve 相的
// sessionFileHasConversationContent 与树渲染的 runtimeSessionTranscriptBlank）
// 换用有界探测后，判定与被替换的 LoadSession+HasContent 同向，且各自保留
// 自己的错误契约（前者错读作无内容，后者错读作保留行）。

func probeSwitchWriteSession(t *testing.T, path string, roles ...provider.Role) {
	t.Helper()
	s := agent.NewSession("sys")
	for _, role := range roles {
		if role == provider.RoleSystem {
			continue // the constructor already carries the system prompt
		}
		s.Add(provider.Message{Role: role, Content: "probe-switch-fixture"})
	}
	if err := s.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
}

func TestSessionFileHasConversationContentBounded(t *testing.T) {
	t.Run("dag route with user message", func(t *testing.T) {
		t.Setenv(agent.SessionLogSchemaEnv, "v2")
		path := filepath.Join(t.TempDir(), "s.jsonl")
		probeSwitchWriteSession(t, path, provider.RoleSystem, provider.RoleUser)
		if !sessionFileHasConversationContent(path) {
			t.Fatal("user message must read as content")
		}
	})
	t.Run("dag route system only", func(t *testing.T) {
		t.Setenv(agent.SessionLogSchemaEnv, "v2")
		path := filepath.Join(t.TempDir(), "s.jsonl")
		probeSwitchWriteSession(t, path, provider.RoleSystem)
		if sessionFileHasConversationContent(path) {
			t.Fatal("system-only must read as no content")
		}
	})
	t.Run("default flat route with user message", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		probeSwitchWriteSession(t, path, provider.RoleSystem, provider.RoleUser)
		if !sessionFileHasConversationContent(path) {
			t.Fatal("user message must read as content on the flat route")
		}
	})
	t.Run("undecodable reads as no content", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(path, []byte("not a transcript"), 0o644); err != nil {
			t.Fatal(err)
		}
		if sessionFileHasConversationContent(path) {
			t.Fatal("the replaced LoadSession branch read errors as false; the probe must too")
		}
	})
	t.Run("empty path", func(t *testing.T) {
		if sessionFileHasConversationContent("  ") {
			t.Fatal("empty path must read as no content")
		}
	})
}

func TestRuntimeSessionTranscriptBlankBounded(t *testing.T) {
	t.Run("content session keeps the row", func(t *testing.T) {
		t.Setenv(agent.SessionLogSchemaEnv, "v2")
		path := filepath.Join(t.TempDir(), "s.jsonl")
		probeSwitchWriteSession(t, path, provider.RoleSystem, provider.RoleUser)
		if runtimeSessionTranscriptBlank(path) {
			t.Fatal("a content-bearing transcript is not blank")
		}
	})
	t.Run("system-only session is blank", func(t *testing.T) {
		t.Setenv(agent.SessionLogSchemaEnv, "v2")
		path := filepath.Join(t.TempDir(), "s.jsonl")
		probeSwitchWriteSession(t, path, provider.RoleSystem)
		if !runtimeSessionTranscriptBlank(path) {
			t.Fatal("a system-only transcript is blank")
		}
	})
	t.Run("undecodable keeps the row", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(path, []byte("{{{ nope"), 0o644); err != nil {
			t.Fatal(err)
		}
		if runtimeSessionTranscriptBlank(path) {
			t.Fatal("undecodable must keep the row (the replaced LoadSession error branch returned false)")
		}
	})
	t.Run("zero-byte and missing are blank", func(t *testing.T) {
		dir := t.TempDir()
		zero := filepath.Join(dir, "zero.jsonl")
		if err := os.WriteFile(zero, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if !runtimeSessionTranscriptBlank(zero) {
			t.Fatal("zero-byte stub must read blank")
		}
		if !runtimeSessionTranscriptBlank(filepath.Join(dir, "gone.jsonl")) {
			t.Fatal("missing file must read blank")
		}
	})
}
