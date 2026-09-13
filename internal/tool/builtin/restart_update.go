package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(restartAndUpdate{}) }

// restartAndUpdate publishes a locally built desktop release as a new version and
// relaunches through the launcher (task 81). It is desktop-only: the work belongs to
// the host, reached through the call context. A host that cannot swap its own install
// simply binds no implementation, and the tool reports that as unavailable instead of
// failing halfway through.
type restartAndUpdate struct{}

func (restartAndUpdate) Name() string { return "restart_and_update" }

// ReadOnly is false: this publishes a new install version and ends the process.
func (restartAndUpdate) ReadOnly() bool { return false }

func (restartAndUpdate) Description() string {
	return "Restart the desktop app onto a locally built release. Use it only when the user asked for a restart-and-update: the app closes, the staged build is published as a new install version, and the launcher brings the app back with tabs restored. Refuses while a turn is running. It returns as soon as the version swap is committed — the restart itself happens a moment later, so a success must not be retried."
}

func (restartAndUpdate) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"additionalProperties":false,
"properties":{
  "source_dir":{"type":"string","maxLength":1024,"description":"Directory holding the staged build (reasonix-desktop.exe, reasonix-cli.exe, reasonix-update-helper.exe, reasonix-launcher.exe). Omit to use the install root's staging directory."},
  "version":{"type":"string","maxLength":64,"description":"Version name to publish, for example v1.38.4. Omit to let the host derive one."}
}
}`)
}

func (restartAndUpdate) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var request struct {
		SourceDir string `json:"source_dir"`
		Version   string `json:"version"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return "", fmt.Errorf("invalid restart_and_update args: %w", err)
		}
	}
	updater, ok := tool.RestartUpdaterFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("restart_and_update is unavailable outside the desktop app")
	}
	return updater.RestartAndUpdate(ctx, strings.TrimSpace(request.SourceDir), strings.TrimSpace(request.Version))
}
