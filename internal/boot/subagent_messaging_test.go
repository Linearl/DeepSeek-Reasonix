package boot

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
)

// Task 616 acceptance: the send_message tool rides the
// experimental_subagent_messaging switch. Default (nil) is ON — the user's
// explicit ruling and the recorded exception to the default-off fork rule;
// an explicit false removes the tool from the parent registry entirely.

func registeredBootToolNames(t *testing.T, tomlBody string) map[string]bool {
	t.Helper()
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	writeFile(t, dir, "reasonix.toml", tomlBody)
	registerBootTokenProfileTestProvider()
	setBootTokenProfileTestProvider(t, testutil.NewMock("messaging", testutil.Turn{Text: "done"}))
	ctrl, err := Build(context.Background(), Options{Sink: event.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()
	names := map[string]bool{}
	for _, entry := range ctrl.AllToolContractEntries() {
		names[entry.Name] = true
	}
	return names
}

const messagingBootToml = `
default_model = "test-model"
[[providers]]
name = "test-model"
kind = "boot-token-profile-test"
model = "x"
`

func TestBootRegistersSendMessageByDefault(t *testing.T) {
	registered := registeredBootToolNames(t, messagingBootToml)
	if !registered["send_message"] {
		t.Fatal("send_message missing from the default (switch nil = on) parent registry")
	}
	if !registered["task"] {
		t.Fatal("task tool missing — boot assembly changed under the messaging test")
	}
}

func TestBootSwitchOffRemovesSendMessage(t *testing.T) {
	registered := registeredBootToolNames(t, messagingBootToml+`
[agent]
experimental_subagent_messaging = false
`)
	if registered["send_message"] {
		t.Fatal("send_message registered while experimental_subagent_messaging = false (off must be byte-identical to the pre-616 surface)")
	}
	if !registered["task"] {
		t.Fatal("switch off must not affect the task tool itself")
	}
	if strings.Contains(messagingBootToml, "send_message") {
		t.Fatal("guard: base toml unexpectedly mentions send_message")
	}
}
