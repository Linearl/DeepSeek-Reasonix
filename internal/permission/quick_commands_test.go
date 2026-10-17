package permission

import (
	"encoding/json"
	"testing"
)

// 任务 960：set_quick_commands 是写 config 类工具（builtinSafety 登记
// ReadOnly=false），权限面与 heartbeat_task_upsert 同类——走 Policy writer
// fallback，无独立声明。本测试钉死三态：mode=ask 弹批准（不静默放行）、
// mode=deny 拒绝、allow/deny 规则按名生效且 deny 优先。
func TestQuickCommandsPermissionGate(t *testing.T) {
	args := json.RawMessage(`{"action":"set","entries":[{"title":"A","text":"a"}]}`)

	if got := New("ask", nil, nil, nil).Decide("set_quick_commands", false, args); got != Ask {
		t.Fatalf("writer fallback under mode=ask = %v, want Ask (a config-writing tool must prompt, never silently allow)", got)
	}
	if got := New("deny", nil, nil, nil).Decide("set_quick_commands", false, args); got != Deny {
		t.Fatalf("writer fallback under mode=deny = %v, want Deny", got)
	}
	if got := New("allow", nil, nil, nil).Decide("set_quick_commands", false, args); got != Allow {
		t.Fatalf("writer fallback under mode=allow = %v, want Allow", got)
	}
	// Rule-by-name: an allow entry lifts the prompt; a deny entry still wins.
	if got := New("ask", []string{"set_quick_commands"}, nil, nil).Decide("set_quick_commands", false, args); got != Allow {
		t.Fatalf("named allow rule = %v, want Allow", got)
	}
	if got := New("allow", []string{"set_quick_commands"}, nil, []string{"set_quick_commands"}).Decide("set_quick_commands", false, args); got != Deny {
		t.Fatalf("named deny rule must beat mode=allow, got %v", got)
	}
}
