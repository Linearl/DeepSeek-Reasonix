package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// tl-1173 P1 (DAG 退役开关化第一步) 的验收回归。判定线：日志已含 DAG 条目
// （probe.dag）的会话终身 native；其余（新会话、裸 checkpoint、schema-1 日志）
// 在构建默认下一律走 schema-1 扁平写。回滚 = 把 defaultNewSessionDAGWriter 翻
// 回 true，或运行时设 REASONIX_SESSION_LOG=v2——两者走同一判定分支，这里用
// v2 钉住回滚语义。

// TestDAGRetirementNewSessionsStayFlatOnDefaultRoute pins the new default:
// a brand-new session saves on the schema-1 flat writer, keeps saving flat on
// later appends, and reloads with its transcript intact.
func TestDAGRetirementNewSessionsStayFlatOnDefaultRoute(t *testing.T) {
	t.Setenv(SessionLogSchemaEnv, "") // follow the build default (flat) explicitly
	path := dagTestSession(t)
	s := dagSavedSession(t, path, "q1", "a1")
	probe, err := probeSessionEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if probe.dag {
		t.Fatal("a new session must not start a DAG log on the default route")
	}
	loaded, err := LoadSession(path)
	if err != nil || strings.Join(dagContents(loaded.Messages), ",") != "sys,q1,a1" {
		t.Fatalf("reload flat session: %v err=%v", dagContents(loaded.Messages), err)
	}
	s.Add(provider.Message{Role: provider.RoleUser, Content: "q2"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	if probe, err := probeSessionEventLog(path); err != nil || probe.dag {
		t.Fatalf("append must stay flat: probe=%+v err=%v", probe, err)
	}
	again, err := LoadSession(path)
	if err != nil || strings.Join(dagContents(again.Messages), ",") != "sys,q1,a1,q2" {
		t.Fatalf("reload after append: %v err=%v", dagContents(again.Messages), err)
	}
}

// TestDAGRetirementLegacyDAGSessionStaysNative pins the frozen half of the
// rollout: a session whose log already carries DAG entries keeps its native
// route under the flat default — it loads, appends, and stays schema 2.
func TestDAGRetirementLegacyDAGSessionStaysNative(t *testing.T) {
	t.Setenv(SessionLogSchemaEnv, "v2") // build the legacy log on the DAG route
	path := dagTestSession(t)
	dagSavedSession(t, path, "q1", "a1")
	if probe, _ := probeSessionEventLog(path); !probe.dag {
		t.Fatal("precondition: session must be a DAG log")
	}
	t.Setenv(SessionLogSchemaEnv, "") // back to the build default (flat)
	loaded, err := LoadSession(path)
	if err != nil || strings.Join(dagContents(loaded.Messages), ",") != "sys,q1,a1" {
		t.Fatalf("legacy DAG reload under flat default: %v err=%v", dagContents(loaded.Messages), err)
	}
	loaded.Add(provider.Message{Role: provider.RoleUser, Content: "q2"})
	if err := loaded.Save(path); err != nil {
		t.Fatal(err)
	}
	probe, err := probeSessionEventLog(path)
	if err != nil || !probe.dag {
		t.Fatalf("legacy DAG session must stay native: probe=%+v err=%v", probe, err)
	}
	again, err := LoadSession(path)
	if err != nil || strings.Join(dagContents(again.Messages), ",") != "sys,q1,a1,q2" {
		t.Fatalf("legacy DAG reload after append: %v err=%v", dagContents(again.Messages), err)
	}
}

// TestDAGRetirementMixedStoreCoexist pins the mixed scenario: a flat session
// and a legacy DAG session side by side in one store each keep their own
// schema across loads and appends on the default route.
func TestDAGRetirementMixedStoreCoexist(t *testing.T) {
	t.Setenv(SessionLogSchemaEnv, "v2")
	dir := t.TempDir()
	dagPath := filepath.Join(dir, "dag.jsonl")
	dagSavedSession(t, dagPath, "d1", "d2")
	t.Setenv(SessionLogSchemaEnv, "") // default (flat) for everything below
	flatPath := filepath.Join(dir, "flat.jsonl")
	dagSavedSession(t, flatPath, "f1", "f2")

	if probe, _ := probeSessionEventLog(flatPath); probe.dag {
		t.Fatal("new session in a mixed store must be flat")
	}
	if probe, _ := probeSessionEventLog(dagPath); !probe.dag {
		t.Fatal("legacy session in a mixed store must stay DAG")
	}
	loadedFlat, err := LoadSession(flatPath)
	if err != nil || strings.Join(dagContents(loadedFlat.Messages), ",") != "sys,f1,f2" {
		t.Fatalf("flat reload: %v err=%v", dagContents(loadedFlat.Messages), err)
	}
	loadedDAG, err := LoadSession(dagPath)
	if err != nil || strings.Join(dagContents(loadedDAG.Messages), ",") != "sys,d1,d2" {
		t.Fatalf("DAG reload: %v err=%v", dagContents(loadedDAG.Messages), err)
	}
	loadedFlat.Add(provider.Message{Role: provider.RoleUser, Content: "f3"})
	if err := loadedFlat.Save(flatPath); err != nil {
		t.Fatal(err)
	}
	loadedDAG.Add(provider.Message{Role: provider.RoleUser, Content: "d3"})
	if err := loadedDAG.Save(dagPath); err != nil {
		t.Fatal(err)
	}
	if probe, _ := probeSessionEventLog(flatPath); probe.dag {
		t.Fatal("flat session must stay flat after append")
	}
	if probe, _ := probeSessionEventLog(dagPath); !probe.dag {
		t.Fatal("DAG session must stay native after append")
	}
	for path, want := range map[string]string{
		flatPath: "sys,f1,f2,f3",
		dagPath:  "sys,d1,d2,d3",
	} {
		s, err := LoadSession(path)
		if err != nil || strings.Join(dagContents(s.Messages), ",") != want {
			t.Fatalf("%s reload: %v err=%v", filepath.Base(path), dagContents(s.Messages), err)
		}
	}
}

// TestDAGRetirementRollbackSwitchRestoresDAGRoute pins the rollback: with the
// switch re-enabled (REASONIX_SESSION_LOG=v2, the same branch the default
// constant flips when rolled back) a brand-new session upgrades to a DAG log
// again, so the retirement is one switch away from the old behavior.
func TestDAGRetirementRollbackSwitchRestoresDAGRoute(t *testing.T) {
	path := dagTestSession(t)
	t.Setenv(SessionLogSchemaEnv, "v2")
	dagSavedSession(t, path, "q1", "a1")
	probe, err := probeSessionEventLog(path)
	if err != nil || !probe.dag {
		t.Fatalf("rollback switch must restore the DAG route: probe=%+v err=%v", probe, err)
	}
	loaded, err := LoadSession(path)
	if err != nil || strings.Join(dagContents(loaded.Messages), ",") != "sys,q1,a1" {
		t.Fatalf("reload after rollback save: %v err=%v", dagContents(loaded.Messages), err)
	}
	if _, ok := loaded.Head(); !ok {
		t.Fatal("rolled-back session must expose a DAG head")
	}
}
