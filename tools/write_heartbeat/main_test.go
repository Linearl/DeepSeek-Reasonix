package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// 任务 823 反哺素材落地验收：种子工具对既有配置的 Unmarshal 错误不再吞——
// loadEnvelope 显式报错（上游原版 `_ = json.Unmarshal` 会静默丢掉全部既有
// 任务后覆盖发布）。
func TestLoadEnvelopeRejectsBrokenConfig(t *testing.T) {
	for name, raw := range map[string]string{
		"syntax error":    `{"tasks": [`,
		"wrong type":      `{"tasks": [{"id": "a", "createdAt": "yesterday"}]}`,
		"tasks not array": `{"tasks": "all"}`,
		"envelope string": `"just a string"`,
	} {
		_, err := loadEnvelope([]byte(raw))
		if err == nil {
			t.Fatalf("%s: loadEnvelope accepted broken config, want explicit error", name)
		}
		if !strings.Contains(err.Error(), "not valid config") {
			t.Fatalf("%s: error=%q want the not-valid-config prefix", name, err)
		}
	}
}

func TestLoadEnvelopeAcceptsEmptyAndValid(t *testing.T) {
	// 空文件（首次播种）→ 空任务列表。
	data, err := loadEnvelope(nil)
	if err != nil {
		t.Fatalf("empty bytes: %v", err)
	}
	if len(data.Tasks) != 0 {
		t.Fatalf("empty bytes tasks=%d, want 0", len(data.Tasks))
	}

	// 合法既有配置 → envelope 原样保留（schemaVersion/revision 不被降级）。
	raw := `{"schemaVersion":4,"revision":7,"tasks":[{"id":"a","interval":"5m","enabled":true}]}`
	data, err = loadEnvelope([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if data.SchemaVersion != 4 || data.Revision != 7 || len(data.Tasks) != 1 {
		t.Fatalf("envelope=%+v want schema 4 revision 7 one task", data)
	}
}

// 播种后的 envelope 可发布：marshal 往返不丢字段。
func TestSeededEnvelopeRoundTrips(t *testing.T) {
	data, err := loadEnvelope([]byte(`{"schemaVersion":4,"revision":7,"tasks":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	data.Revision++
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var back envelope
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("published envelope not parseable: %v", err)
	}
	if back.Revision != 8 || back.SchemaVersion != 4 {
		t.Fatalf("round trip=%+v want revision 8 schema 4", back)
	}
}
