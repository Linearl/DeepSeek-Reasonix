// Command write_heartbeat is an external editor for heartbeat-tasks.json —
// the writer class the 任务 229 G5 audit identified as the one remaining
// clobber window: the app guards its own writes with filelock(.lock) +
// digest CAS + atomic rename, but external editors used to write bare.
// The external-write contract (same lock, atomic publish, envelope
// preserved) closes that window; see desktop/heartbeat_store.go for the
// app-side half of the contract.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/filelock"
	"reasonix/internal/fileutil"
	fileencoding "reasonix/internal/fileutil/encoding"
)

type Task struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Prompt          string `json:"prompt"`
	Interval        string `json:"interval"`
	Enabled         bool   `json:"enabled"`
	TopicID         string `json:"topicId,omitempty"`
	LastRunAt       int64  `json:"lastRunAt,omitempty"`
	CreatedAt       int64  `json:"createdAt,omitempty"`
	ApprovalMode    string `json:"approvalMode"`
	TimeWindowStart string `json:"timeWindowStart,omitempty"`
	TimeWindowEnd   string `json:"timeWindowEnd,omitempty"`
}

func main() {
	base := config.MemoryUserDir()
	if base == "" {
		base = "."
	}
	path := filepath.Join(base, "heartbeat-tasks.json")

	// 任务 229 G5 external-write contract, half one: hold the SAME lock the
	// app holds (heartbeat_store.go writeTasks) for the whole read-modify-
	// write, so an app save can no longer land between our read and our write.
	lockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release, err := filelock.Acquire(lockCtx, path+".lock")
	if err != nil {
		fmt.Println("Lock error:", err)
		os.Exit(1)
	}
	defer release()

	// Read existing (under the lock).
	b, _ := fileencoding.ReadFileUTF8(path)
	var data struct {
		SchemaVersion int    `json:"schemaVersion"`
		Revision      int    `json:"revision"`
		Tasks         []Task `json:"tasks"`
	}
	if len(b) > 0 {
		_ = json.Unmarshal(b, &data)
	}
	if data.Tasks == nil {
		data.Tasks = []Task{}
	}

	now := time.Now().UnixMilli()
	data.Tasks = append(data.Tasks, Task{
		ID:        "greeting_hello_001",
		Title:     "打个招呼",
		Prompt:    "你好！请用一段友好的话介绍一下你自己，然后用中文打个招呼。",
		Interval:  "2m",
		Enabled:   true,
		CreatedAt: now,
	}, Task{
		ID:        "daily_check_002",
		Title:     "每日检查",
		Prompt:    "检查当前项目的最新改动和状态，总结需要关注的事项。",
		Interval:  "1h",
		Enabled:   true,
		CreatedAt: now,
	})

	// Contract, half two: keep the app's envelope (schemaVersion untouched,
	// revision advanced) so the app's digest-CAS sees a clean hand-off instead
	// of a schema downgrade, and publish atomically like the app does.
	data.Revision++
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Println("Marshal error:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Println("Mkdir error:", err)
		os.Exit(1)
	}
	if err := fileutil.AtomicWriteFile(path, out, 0o644); err != nil {
		fmt.Println("Write error:", err)
		os.Exit(1)
	}
	fmt.Println("Done! Added 2 tasks.")
	fmt.Println("File:", path)
}
