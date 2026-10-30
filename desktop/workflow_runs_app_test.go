package main

import (
	"strings"
	"testing"

	"reasonix/internal/workflow"
)

// 任务 934 验收：定义 → 面板 DAG wire 的转换面（686 面板渲染与引擎执行
// 同一张图的契约）。运行登记/合流推送的编排行为由 internal/workflow 服务
// 测试与前端 harness 覆盖，这里只钉纯转换。
func TestWorkflowDagWireFromDef(t *testing.T) {
	wf, err := workflow.Parse([]byte(`
version: 1
name: conv-wf
description: conversion fixture
nodes:
  - id: survey
    type: phase
    label: survey
  - id: plan
    type: subagent
    prompt: make a plan
    description: planning node
  - id: deliver
    type: phase
    label: deliver
  - id: gate-tests
    type: gate
    command: "go test ./..."
  - id: fixer
    type: subagent
    prompt: fix it
  - id: gather
    type: join
  - id: confirm
    type: confirm
    target: plan
edges:
  - from: plan
    to: gate-tests
  - from: gate-tests
    to: fixer
    when: fail
  - from: gate-tests
    to: gather
    when: pass
  - from: fixer
    to: gather
  - from: gather
    to: confirm
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	wire := workflowDagWireFromDef(wf)
	if wire == nil || wire.ID != "conv-wf" || wire.Name != "conv-wf" {
		t.Fatalf("identity mismatch: %+v", wire)
	}
	if strings.Join(wire.Phases, ",") != "survey,deliver" {
		t.Fatalf("phases must collect ordered unique labels, got %v", wire.Phases)
	}
	if len(wire.Nodes) != len(wf.Nodes) {
		t.Fatalf("node count mismatch: %d vs %d", len(wire.Nodes), len(wf.Nodes))
	}
	byID := map[string]workflowRunDagNodeWire{}
	for _, n := range wire.Nodes {
		byID[n.ID] = n
	}
	if byID["plan"].Kind != "subagent" || byID["plan"].Instruction != "make a plan" {
		t.Fatalf("subagent node must carry kind and prompt instruction: %+v", byID["plan"])
	}
	if byID["plan"].Phase != "survey" {
		t.Fatalf("nodes after a phase marker belong to its label, got %q", byID["plan"].Phase)
	}
	if byID["gate-tests"].Kind != "gate" || byID["gate-tests"].Command != "go test ./..." {
		t.Fatalf("gate node must carry the literal command: %+v", byID["gate-tests"])
	}
	if byID["gate-tests"].Phase != "deliver" {
		t.Fatalf("phase reassignment follows the second marker, got %q", byID["gate-tests"].Phase)
	}
	if byID["confirm"].Kind != "confirm" || !strings.Contains(byID["confirm"].Instruction, "plan") {
		t.Fatalf("confirm node must name its target as the drill-down instruction: %+v", byID["confirm"])
	}
	if byID["fixer"].Phase != "deliver" {
		t.Fatalf("fixer inherits the open phase, got %q", byID["fixer"].Phase)
	}

	labels := map[string]string{}
	for _, e := range wire.Edges {
		labels[e.From+"->"+e.To] = e.Label
	}
	if labels["gate-tests->fixer"] != "fail" || labels["gate-tests->gather"] != "pass" {
		t.Fatalf("gate branch edges must label pass/fail: %+v", labels)
	}
	if labels["plan->gate-tests"] != "" {
		t.Fatalf("always edges carry no label: %+v", labels)
	}
}

func TestWorkflowDagWireFromDefNil(t *testing.T) {
	if workflowDagWireFromDef(nil) != nil {
		t.Fatal("nil definition must map to nil (caller falls back to fixture view)")
	}
}
