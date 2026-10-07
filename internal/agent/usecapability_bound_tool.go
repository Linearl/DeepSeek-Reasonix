package agent

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"time"

	"reasonix/internal/capability"
	"reasonix/internal/config"
	"reasonix/internal/plugin"
	"reasonix/internal/tool"
)

func cachedToolsForSpec(spec plugin.Spec, profile plugin.HostProfile) ([]plugin.CachedTool, bool) {
	cached, keyOK := capability.LoadCachedToolsForSpecs([]plugin.Spec{spec}, profile)
	return cloneCachedTools(cached[spec.Name]), keyOK[spec.Name]
}

func runtimePluginEntry(entry config.PluginEntry) config.PluginEntry {
	out := config.PluginEntry{
		Name:        strings.TrimSpace(entry.Name),
		Concurrency: strings.ToLower(strings.TrimSpace(entry.Concurrency)),
		Source:      entry.Source,
	}
	if entry.AutoStart != nil {
		value := *entry.AutoStart
		out.AutoStart = &value
	}
	return out
}

func cloneCachedTools(in []plugin.CachedTool) []plugin.CachedTool {
	if len(in) == 0 {
		return nil
	}
	out := make([]plugin.CachedTool, len(in))
	copy(out, in)
	for i := range out {
		out[i].Schema = append(json.RawMessage(nil), in[i].Schema...)
	}
	return out
}

func cloneMCPSpec(in plugin.Spec) plugin.Spec {
	out := in
	out.Args = append([]string(nil), in.Args...)
	out.LaunchArgs = append([]string(nil), in.LaunchArgs...)
	out.LauncherIdentityArgs = append([]string(nil), in.LauncherIdentityArgs...)
	out.Env = cloneStringMap(in.Env)
	out.Headers = cloneStringMap(in.Headers)
	if in.ToolTimeouts != nil {
		out.ToolTimeouts = make(map[string]time.Duration, len(in.ToolTimeouts))
		maps.Copy(out.ToolTimeouts, in.ToolTimeouts)
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

// runtimeBoundMCPTool keeps the provider-visible MCP adapter unchanged while
// binding execution to the current controller runtime. The underlying Host may
// be shared by sibling tabs, so a server name alone must never authorize reuse.
type runtimeBoundMCPTool struct {
	proxy      *UseCapabilityTool
	target     tool.Tool
	server     string
	authorized bool
}

func (b *runtimeBoundMCPTool) Name() string              { return b.target.Name() }
func (b *runtimeBoundMCPTool) Description() string       { return b.target.Description() }
func (b *runtimeBoundMCPTool) Schema() json.RawMessage   { return b.target.Schema() }
func (b *runtimeBoundMCPTool) ReadOnly() bool            { return b.target.ReadOnly() }
func (b *runtimeBoundMCPTool) MCPServerAuthorized() bool { return b.authorized }
func (b *runtimeBoundMCPTool) MCPServerName() string     { return b.server }
func (b *runtimeBoundMCPTool) MCPRawToolName() string    { return mcpRawToolName(b.target) }
func (b *runtimeBoundMCPTool) MCPDestructiveHint() bool  { return mcpDestructiveHint(b.target) }
func (b *runtimeBoundMCPTool) MCPVisibleToolName() string {
	if meta, ok := b.target.(tool.MCPVisibleMetadata); ok {
		return meta.MCPVisibleToolName()
	}
	return b.MCPRawToolName()
}
func (b *runtimeBoundMCPTool) MCPPackageName() string {
	if meta, ok := b.target.(tool.MCPPackageMetadata); ok {
		return meta.MCPPackageName()
	}
	return ""
}

func (b *runtimeBoundMCPTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var out string
	err := b.proxy.withRuntimeBoundMCP(ctx, b.server, b.target, func() error {
		var execErr error
		out, execErr = b.target.Execute(ctx, args)
		return execErr
	})
	return out, err
}

func (b *runtimeBoundMCPTool) ExecuteWithImages(ctx context.Context, args json.RawMessage) (string, []string, error) {
	var out string
	var images []string
	err := b.proxy.withRuntimeBoundMCP(ctx, b.server, b.target, func() error {
		if imageTool, ok := b.target.(tool.ImageTool); ok {
			var execErr error
			out, images, execErr = imageTool.ExecuteWithImages(ctx, args)
			return execErr
		}
		var execErr error
		out, execErr = b.target.Execute(ctx, args)
		return execErr
	})
	return out, images, err
}

func mcpRawToolName(target tool.Tool) string {
	if meta, ok := target.(tool.MCPMetadata); ok {
		return meta.MCPRawToolName()
	}
	return ""
}
