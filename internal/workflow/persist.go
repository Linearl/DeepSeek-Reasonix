package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"reasonix/internal/fileutil"
)

// 任务 1316 —— run 级持久化（断点续跑的落盘面）。
//
// 形态沿用既有存储语义，不造新机制：
//   - 目录收口 = config.WorkflowsDir() 下的 runs/ 侧车（与定义同目录，应用级；
//     面板与 workflow_status 的应用级视角同源）；
//   - 一个 run 一个目录一个 run.json，快照覆盖写（fileutil.AtomicWriteFile），
//     无事件 journal —— 节点六态事件流已有 UI 实时通道，持久化只服务「重启后
//     状态恢复」一个目的；
//   - schemaVersion 前向保护（heartbeat run-history sidecar 同款语义）：读到
//     本二进制不认识的版本，跳过恢复、拒绝覆盖，绝不写坏未来格式；
//   - 损坏文件跳过不炸目录（store.list 同款语义）。
//
// 快照是引擎 runState 的序列化投影（单一语义一份真相），引擎侧在节点终态
// 投影、服务侧落盘；恢复时灌回 runState 继续调度。

// snapshotSchemaVersion 是 run.json 的当前盘上格式版本。
const snapshotSchemaVersion = 1

// maxNodeOutputBytes 是单节点 output 入快照的上限（尾部截断）。超限节点的
// 下游 interpolation 会拼出残缺 prompt，恢复时该节点整节点重置重跑。
const maxNodeOutputBytes = 64 << 10

// nodeSnapshot 是一个节点在快照里的恢复面。只落恢复相关节点（done/skipped）；
// running/pending/fixer 不落——恢复规则把它们全部重置重跑。
type nodeSnapshot struct {
	NodeType string `json:"nodeType,omitempty"`
	// LastEventState 是该节点最后一次六态事件（服务层 recordNode 记录），
	// 恢复时灌回面板渲染；skip 节点无事件故为空（保持 queued-gray，与原 run 一致）。
	LastEventState string `json:"lastEventState,omitempty"`
	// ChangedAt 是该节点最后一次六态事件的时刻（服务层 recordNode 记录，与
	// LastEventState 同源同刻；tl1336）。恢复时回填运行登记，workflow_status
	// 与面板读到落盘时的真实时刻而非 Go 零值（UnixMilli=-62135596800000）。
	// 无事件节点与 tl1336 之前的旧快照为零值（omitzero 不落盘），视图侧按「未知」省略。
	ChangedAt time.Time `json:"changedAt,omitzero"`
	// Status 是引擎调度态序列化："done" | "skipped"（其余值不落盘不恢复）。
	Status string `json:"status"`
	// Terminal 是终态类别："passed" | "gate_failed" | "skipped"。
	Terminal   string `json:"terminal,omitempty"`
	GatePassed *bool  `json:"gatePassed,omitempty"`
	Round      int    `json:"round,omitempty"`
	Detail     string `json:"detail,omitempty"`
	// Output 是可插值输出（超 maxNodeOutputBytes 尾部截断并置 OutputTruncated）。
	Output          string          `json:"output,omitempty"`
	OutputTruncated bool            `json:"outputTruncated,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	Findings        []Finding       `json:"findings,omitempty"`
}

// runSnapshot 是一次运行的完整恢复面（run.json 的盘上形态）。
type runSnapshot struct {
	SchemaVersion int    `json:"schemaVersion"`
	RunID         string `json:"runId"`
	Workflow      string `json:"workflow"`
	// DefFingerprint 是装载定义的指纹；resume 时与现定义重算值比对，不匹配拒绝。
	DefFingerprint string     `json:"defFingerprint"`
	State          string     `json:"state"` // running | completed | failed
	StartedAt      time.Time  `json:"startedAt"`
	EndedAt        *time.Time `json:"endedAt,omitempty"`
	Err            string     `json:"err,omitempty"`
	Cancelled      bool       `json:"cancelled,omitempty"`

	Phase      string `json:"phase,omitempty"`
	FailedNode string `json:"failedNode,omitempty"`
	GateRound  int    `json:"gateRound,omitempty"`

	// NodeOrder 是节点首见序（= 文档声明序），恢复 RunRecord.order 用。
	NodeOrder []string                `json:"nodeOrder,omitempty"`
	Nodes     map[string]nodeSnapshot `json:"nodes,omitempty"`

	Bag    []Finding `json:"bag,omitempty"`
	Verifs []string  `json:"verifs,omitempty"`

	Report *WorkflowReport `json:"report,omitempty"`
}

// CheckpointData 是引擎递给宿主的投影（引擎不感知磁盘；落盘是 Service 的
// 职责）。字段即 runState 的恢复相关子集。
type CheckpointData struct {
	Nodes      map[string]nodeSnapshot
	NodeOrder  []string
	Bag        []Finding
	Verifs     []string
	Phase      string
	FailedNode string
	GateRound  int
}

// defFingerprint 计算定义指纹（canonical JSON 的 sha256）。Parse 后的结构体
// marshal 是规范形态，同一文档重算值稳定。
func defFingerprint(wf *Workflow) string {
	raw, err := json.Marshal(wf)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// truncateTail 把 s 截到至多 max 字节（保留尾部，gate 输出习惯与诊断价值都
// 在末尾），并在 rune 边界切开避免半个 UTF-8 字符。
func truncateTail(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	tail := s[len(s)-max:]
	// 尾部起点若落在多字节 rune 中间，首字节必是 continuation byte
	// （0b10xxxxxx）——向后丢弃最多 3 字节直到起点是合法 rune 边界。
	for i := 0; i < 3 && len(tail) > 0 && tail[0]&0xC0 == 0x80; i++ {
		tail = tail[1:]
	}
	return tail, true
}

// 恢复侧目录收口。dir 为空 = 不落盘（user state dir 无法解析的极端环境），
// 全部方法退化为 no-op / 明确错误。

func runsDir(workflowsDir string) string {
	if workflowsDir == "" {
		return ""
	}
	return filepath.Join(workflowsDir, "runs")
}

func runSnapshotPath(dir, runID string) string {
	return filepath.Join(dir, runID, "run.json")
}

// saveRunSnapshot 原子写一份快照。dir 为空 = no-op（持久化是增强不是门禁，
// 写不了盘的极端环境 run 照跑）。
func saveRunSnapshot(dir string, snap *runSnapshot) error {
	if dir == "" {
		return nil
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("workflow: snapshot encode: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(runSnapshotPath(dir, snap.RunID)), 0o755); err != nil {
		return fmt.Errorf("workflow: snapshot store: %w", err)
	}
	if err := fileutil.AtomicWriteFile(runSnapshotPath(dir, snap.RunID), raw, 0o644); err != nil {
		return fmt.Errorf("workflow: snapshot store: %w", err)
	}
	return nil
}

// loadRunSnapshot 读一份快照并做版本校验。损坏 / 未来版本返回错误（调用方
// 跳过该 run，不炸目录）。
func loadRunSnapshot(dir, runID string) (*runSnapshot, error) {
	if dir == "" {
		return nil, os.ErrNotExist
	}
	raw, err := os.ReadFile(runSnapshotPath(dir, runID))
	if err != nil {
		return nil, err
	}
	var snap runSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("workflow: snapshot %s: %w", runID, err)
	}
	if snap.SchemaVersion > snapshotSchemaVersion {
		return nil, fmt.Errorf("workflow: snapshot %s schemaVersion %d is newer than this binary supports (%d); upgrade Reasonix before resuming", runID, snap.SchemaVersion, snapshotSchemaVersion)
	}
	if snap.RunID == "" {
		snap.RunID = runID
	}
	return &snap, nil
}

// listRunSnapshotIDs 枚举 runs/ 下的全部 runID（目录名序）。
func listRunSnapshotIDs(dir string) []string {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

// removeRunSnapshot 删一个 run 的快照目录。
func removeRunSnapshot(dir, runID string) {
	if dir == "" {
		return
	}
	_ = os.RemoveAll(filepath.Join(dir, runID))
}
