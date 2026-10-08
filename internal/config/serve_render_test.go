package config

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

const enrollToken = "a1b2c3d4e5f6a7b8a1b2c3d4e5f6a7b8a1b2c3d4e5f6a7b8a1b2c3d4e5f6a7b8"

// enrollLikeConfig mimics what `reasonix bus enroll` leaves in memory before
// cfg.Save(): bus enabled, one role token, plus a worker pool config.
func enrollLikeConfig() *Config {
	c := Default()
	c.Serve.BusMCP.Enabled = true
	c.Serve.BusMCP.Roles = map[string]string{"dev": enrollToken}
	c.Serve.BusWorker.Enabled = true
	c.Serve.BusWorker.Command = "zcode"
	return c
}

// TestServeBusSectionsRenderAndRoundTrip: the renderer must serialize
// [serve.bus_mcp] and [serve.bus_worker] (task 432: enroll set them in memory
// and Save silently dropped them), the output must parse, and the decoded
// values must match. The project scope must never carry the bus tables —
// the roles map holds bearer tokens.
func TestServeBusSectionsRenderAndRoundTrip(t *testing.T) {
	c := enrollLikeConfig()

	user := RenderTOMLForScope(c, RenderScopeUser)
	for _, want := range []string{
		"[serve]",
		"[serve.bus_mcp]",
		"[serve.bus_worker]",
		"enabled = true",
		`roles = { dev = "` + enrollToken + `" }`,
		`command = "zcode"`,
	} {
		if !strings.Contains(user, want) {
			t.Fatalf("rendered user config missing %q:\n%s", want, user)
		}
	}

	project := RenderTOMLForScope(c, RenderScopeProject)
	for _, leaked := range []string{"[serve]", "bus_mcp", "bus_worker", enrollToken} {
		if strings.Contains(project, leaked) {
			t.Fatalf("project render leaked serve/bus content %q:\n%s", leaked, project)
		}
	}

	var decoded Config
	if _, err := toml.Decode(user, &decoded); err != nil {
		t.Fatalf("rendered user config does not parse: %v\n---\n%s", err, user)
	}
	if !decoded.Serve.BusMCP.Enabled {
		t.Fatal("round trip lost [serve.bus_mcp] enabled")
	}
	if got := decoded.Serve.BusMCP.Roles["dev"]; got != enrollToken {
		t.Fatalf("round trip role dev token = %q, want enrolled token", got)
	}
	if !decoded.Serve.BusWorker.Enabled || decoded.Serve.BusWorker.Command != "zcode" {
		t.Fatalf("round trip lost [serve.bus_worker] config: %+v", decoded.Serve.BusWorker)
	}
}

// TestBusEnrollSaveReloadKeepsRole is the enroll→save→reload regression at the
// config layer: set [serve.bus_mcp] the way `reasonix bus enroll` does, save to
// the real user config path, load the file back from disk, and assert the role
// token survived. Failing this test is how the task 432 silent drop presents.
func TestBusEnrollSaveReloadKeepsRole(t *testing.T) {
	isolateUserConfigHome(t)

	cfg := enrollLikeConfig()
	userPath := userConfigPath()
	if userPath == "" {
		t.Fatal("no user config path in isolated home")
	}
	if err := cfg.SaveTo(userPath); err != nil {
		t.Fatalf("SaveTo user config: %v", err)
	}

	onDisk, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("read saved user config: %v", err)
	}
	if !strings.Contains(string(onDisk), "[serve.bus_mcp]") {
		t.Fatalf("saved user config has no [serve.bus_mcp] section — renderer dropped it:\n%s", onDisk)
	}

	reloaded, err := LoadUserConfigReadOnly()
	if err != nil {
		t.Fatalf("reload user config: %v", err)
	}
	if !reloaded.Serve.BusMCP.Enabled {
		t.Fatal("reloaded config: [serve.bus_mcp] enabled = false, want true")
	}
	if got := reloaded.Serve.BusMCP.Roles["dev"]; got != enrollToken {
		t.Fatalf("reloaded role dev token = %q, want enrolled token", got)
	}
	if !reloaded.Serve.BusWorker.Enabled {
		t.Fatal("reloaded config: [serve.bus_worker] enabled = false, want true")
	}
}

// TestServeSectionDefaultsRoundTrip: the always-rendered [serve] block with a
// zero-value Serve struct must decode back to that zero value — commented
// examples must never invent config.
func TestServeSectionDefaultsRoundTrip(t *testing.T) {
	c := Default()
	rendered := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(rendered, "[serve]") {
		t.Fatalf("user render missing [serve] section:\n%s", rendered)
	}
	var decoded Config
	if _, err := toml.Decode(rendered, &decoded); err != nil {
		t.Fatalf("rendered user config does not parse: %v\n---\n%s", err, rendered)
	}
	if !reflect.DeepEqual(decoded.Serve, ServeConfig{}) {
		t.Fatalf("default render invented [serve] config: %+v", decoded.Serve)
	}
}

// 任务540：experimental_gc_child_session 默认关（铁律 2）——Default 零值，
// 且默认渲染只以注释示例出现（不是活键），关闭态的配置文件语义与 540 之前逐字一致。
func TestGCChildSessionDefaultsOffAndRendersAsComment(t *testing.T) {
	c := Default()
	if c.Serve.ExperimentalGCChildSession {
		t.Fatal("experimental_gc_child_session must default to false (iron rule 2)")
	}
	rendered := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(rendered, "# experimental_gc_child_session = false") {
		t.Fatalf("default render missing the commented example:\n%s", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if strings.HasPrefix(line, "experimental_gc_child_session") {
			t.Fatalf("default render emitted a live key (must stay a comment while off):\n%s", line)
		}
	}
}

// 任务540：显式开启后渲染活键并 round-trip。
func TestGCChildSessionExplicitOnRendersAndRoundTrips(t *testing.T) {
	c := Default()
	c.Serve.ExperimentalGCChildSession = true
	rendered := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(rendered, "experimental_gc_child_session = true") {
		t.Fatalf("explicit-on render missing the live key:\n%s", rendered)
	}
	var decoded Config
	if _, err := toml.Decode(rendered, &decoded); err != nil {
		t.Fatalf("rendered user config does not parse: %v\n---\n%s", err, rendered)
	}
	if !decoded.Serve.ExperimentalGCChildSession {
		t.Fatal("round trip lost experimental_gc_child_session = true")
	}
}

// 任务540：toml 直接解析该键。
func TestGCChildSessionParsesFromTOML(t *testing.T) {
	cfg := Default()
	if _, err := toml.Decode("[serve]\nexperimental_gc_child_session = true\n", cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !cfg.Serve.ExperimentalGCChildSession {
		t.Fatal("toml key experimental_gc_child_session = true did not decode")
	}
}
