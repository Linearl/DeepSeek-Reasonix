package event

// WorkflowNodeState is the machine-readable node state on WorkflowNode
// events (task 685). The six states are the whole contract with the 686
// visualization panel: queued (gray) → running (pulse) → one of the four
// terminals completed / gate_passed (green) / unconfirmed (yellow) /
// failed (red).
type WorkflowNodeState string

const (
	WorkflowNodeQueued      WorkflowNodeState = "queued"
	WorkflowNodeRunning     WorkflowNodeState = "running"
	WorkflowNodeCompleted   WorkflowNodeState = "completed"
	WorkflowNodeUnconfirmed WorkflowNodeState = "unconfirmed"
	WorkflowNodeFailed      WorkflowNodeState = "failed"
	WorkflowNodeGatePassed  WorkflowNodeState = "gate_passed"
)

// WorkflowNodeInfo is the payload on WorkflowNode events: one node state
// transition inside one workflow run. Phase is the label of the phase node
// currently open (empty before the first phase); Round is the gate attempt
// number on gate events (0 elsewhere); Detail is a short diagnostic (exit
// code, failure summary) — never the full output.
type WorkflowNodeInfo struct {
	RunID     string            `json:"runId"`
	Workflow  string            `json:"workflow"`
	NodeID    string            `json:"nodeId"`
	NodeType  string            `json:"nodeType"`
	State     WorkflowNodeState `json:"state"`
	Phase     string            `json:"phase,omitempty"`
	Round     int               `json:"round,omitempty"`
	Detail    string            `json:"detail,omitempty"`
}
