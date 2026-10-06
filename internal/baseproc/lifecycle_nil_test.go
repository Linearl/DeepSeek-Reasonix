package baseproc

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 任务521：ManagedClient 的任何 nil 组合都不得 panic，且必须 fail-closed
// 回落 inline/本地路径（工具调用 panic 致 app 崩溃的用户实测修复）。
//
// 三种 nil 注入，表驱动，逐项对应修复矩阵（见 lifecycle.go target() 注释）：
//
//	注入1：半构造视图——m 缺失的 *ManagedClient 直接经 BaseClient 接口流出；
//	注入2：typed-nil receiver——(*ManagedClient)(nil) 装进接口后 bc==nil 判不出，
//	       第一字段解引用即 panic（typed-nil 陷阱本体）；
//	注入3：各 state × nil remote——markDead/Close 把 remote 置 nil 后 target 的
//	       竞态窗口，外加真实 harness 的已关闭视图。
//
// fail-closed 的判定口径：target() 绝不返回 (nil, nil)；退化视图的
// Mode()=inline（消费门直接走本地注册表路径）；工具面走 inline 时零 Surface
// 答 ErrNotWired（既有 ErrNotWired→本地链），带 Surface 就地本地执行。

// nilInjectedViewCases 注入1/注入2 共用的视图构造与期望。
type nilInjectedViewCase struct {
	name string
	// view 为 nil 表示注入 typed-nil receiver（注入2）；非 nil 为半构造视图（注入1）。
	view *ManagedClient
	// wantTargetErr 为 target() 的期望错误；nil 表示期望 (inline, nil)。
	wantTargetErr error
	// wantLocalExec 为 true 时要求 ToolCall 经 inline Surface 本地执行成功
	// （而非 ErrNotWired）；仅对带 Surface 的注入有意义。
	wantLocalExec bool
}

func TestManagedClientNilManagerViewFallsBackToLocal(t *testing.T) {
	surface := inlineOnlySurface()
	cases := []nilInjectedViewCase{
		{
			name: "注入1 半构造视图 m缺失 zero inline",
			view: &ManagedClient{},
		},
		{
			name:          "注入1b 半构造视图 m缺失 带Surface inline",
			view:          &ManagedClient{inline: InlineBaseClient{ServerVersion: "t521", Surface: surface}},
			wantLocalExec: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var bc BaseClient = tc.view
			if bc == nil {
				t.Fatal("半构造视图装箱后不应为 nil 接口（前提不成立）")
			}
			// Mode 是消费门（agent baseToolCall / control baseCatalogEntries）的
			// 第一个接触点：必须答 inline，让门走本地路径。
			if mode := bc.Mode(); mode != ModeInline {
				t.Fatalf("Mode() = %q, want %q", mode, ModeInline)
			}
			// target() 必须 (inline, nil)，绝不 (nil, nil)。
			tgt, err := bc.(*ManagedClient).target()
			if err != nil {
				t.Fatalf("target() err = %v, want nil", err)
			}
			if tgt == nil {
				t.Fatal("target() 返回 (nil, nil)——typed-nil 陷阱复现")
			}
			if _, ok := tgt.(InlineBaseClient); !ok {
				t.Fatalf("target() = %T, want InlineBaseClient（回落本地）", tgt)
			}
			// 工具面 fail-closed：零 Surface → ErrNotWired（消费门转本地注册表）；
			// 带 Surface → 本地执行成功。
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			res, err := bc.ToolCall(ctx, ToolCallParams{CallID: "nil-1", Tool: "inline-only", Args: []byte(`{}`)})
			if tc.wantLocalExec {
				if err != nil {
					t.Fatalf("ToolCall 本地执行 err = %v, want nil", err)
				}
				if res.Content != "ok:inline-only" {
					t.Fatalf("ToolCall content = %q, want 本地 stub 结果", res.Content)
				}
			} else if !errors.Is(err, ErrNotWired) {
				t.Fatalf("ToolCall err = %v, want ErrNotWired（零 Surface 语义）", err)
			}
			// 其余委托方法同样不得 panic。
			if _, err := bc.ToolCatalog(ctx, ToolCatalogParams{Scope: ScopeAll}); tc.wantLocalExec && err != nil {
				t.Fatalf("ToolCatalog 本地执行 err = %v, want nil", err)
			} else if !tc.wantLocalExec && !errors.Is(err, ErrNotWired) {
				t.Fatalf("ToolCatalog err = %v, want ErrNotWired", err)
			}
			if _, err := bc.Hello(ctx, HelloParams{ProtocolVersion: ProtocolVersion}); err != nil {
				t.Fatalf("Hello err = %v, want nil", err)
			}
			if err := bc.Ping(ctx); err != nil {
				t.Fatalf("Ping err = %v, want nil", err)
			}
			if _, err := bc.Attach(ctx, AttachParams{}); !errors.Is(err, ErrNotWired) {
				t.Fatalf("Attach err = %v, want ErrNotWired", err)
			}
			if err := bc.Shutdown(ctx); err != nil {
				t.Fatalf("Shutdown err = %v, want nil（nil manager 时跳过记账）", err)
			}
			if err := bc.Close(); err != nil {
				t.Fatalf("Close err = %v, want nil", err)
			}
		})
	}
}

func TestManagedClientTypedNilReceiverNeverPanics(t *testing.T) {
	cases := []nilInjectedViewCase{
		{
			name:          "注入2 typed-nil receiver (*ManagedClient)(nil) 装箱",
			view:          nil,
			wantTargetErr: errClientClosed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var view *ManagedClient = tc.view
			var bc BaseClient = view
			if bc == nil {
				t.Fatal("typed-nil 陷阱前提不成立：接口判出了 nil")
			}
			// 修复前：bc.Mode() 在第一字段解引用处 panic——即用户实测的崩溃形态。
			if mode := bc.Mode(); mode != ModeInline {
				t.Fatalf("Mode() = %q, want %q（消费门走本地）", mode, ModeInline)
			}
			tgt, err := bc.(*ManagedClient).target()
			if !errors.Is(err, tc.wantTargetErr) {
				t.Fatalf("target() err = %v, want %v", err, tc.wantTargetErr)
			}
			if tgt != nil {
				t.Fatalf("target() tgt = %v, want nil（nil receiver 无本地面可回落）", tgt)
			}
			// 全部委托方法不得 panic：错误向上传播，调用方已有错误分支。
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, err := bc.ToolCall(ctx, ToolCallParams{CallID: "nil-2", Tool: "x"}); !errors.Is(err, errClientClosed) {
				t.Fatalf("ToolCall err = %v, want errClientClosed", err)
			}
			if _, err := bc.ToolCatalog(ctx, ToolCatalogParams{Scope: ScopeAll}); !errors.Is(err, errClientClosed) {
				t.Fatalf("ToolCatalog err = %v, want errClientClosed", err)
			}
			if err := bc.Ping(ctx); !errors.Is(err, errClientClosed) {
				t.Fatalf("Ping err = %v, want errClientClosed", err)
			}
			if err := bc.Shutdown(ctx); !errors.Is(err, errClientClosed) {
				t.Fatalf("Shutdown err = %v, want errClientClosed", err)
			}
			if err := bc.Close(); err != nil {
				t.Fatalf("Close err = %v, want nil", err)
			}
			// State() 不在 BaseClient 接口上：直接以具体类型（nil receiver）调用。
			if st := view.State(); st == "" {
				t.Fatal("State() 返回空串")
			}
		})
	}
}

func TestManagedClientNilRemoteEveryStateFallsBackInline(t *testing.T) {
	surface := inlineOnlySurface()
	// 注入3a：四个 D5 态 × nil remote——markDead/Close 与 target 的竞态窗口
	// （remote_ready 被置 nil remote 后，消费门 Mode 检查与 target 之间）。
	states := []struct {
		name  string
		state baseState
	}{
		{"warming", stateWarming},
		{"remote_ready+nil remote（markDead 竞态窗口）", stateRemoteReady},
		{"degraded", stateDegraded},
		{"stopped", stateStopped},
	}
	for _, st := range states {
		t.Run("注入3a "+st.name, func(t *testing.T) {
			v := &ManagedClient{
				m:      &Manager{state: st.state}, // remote 零值 = nil
				inline: InlineBaseClient{ServerVersion: "t521", Surface: surface},
			}
			if mode := v.Mode(); mode != ModeInline {
				t.Fatalf("Mode() = %q, want %q", mode, ModeInline)
			}
			tgt, err := v.target()
			if err != nil {
				t.Fatalf("target() err = %v, want nil", err)
			}
			if tgt == nil {
				t.Fatal("target() 返回 (nil, nil)")
			}
			if _, ok := tgt.(InlineBaseClient); !ok {
				t.Fatalf("target() = %T, want InlineBaseClient", tgt)
			}
			// fail-closed：本地执行真的发生（不是崩、不是挂）。
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			res, err := v.ToolCall(ctx, ToolCallParams{CallID: "nil-3", Tool: "inline-only", Args: []byte(`{}`)})
			if err != nil {
				t.Fatalf("ToolCall err = %v, want 本地执行成功", err)
			}
			if res.Content != "ok:inline-only" {
				t.Fatalf("ToolCall content = %q, want 本地 stub 结果", res.Content)
			}
		})
	}

	// 注入3b：真实 harness 的已关闭视图——done 视图 target() 答 errClientClosed。
	h := newLifecycleHarness(t, func(rec *spawnRecorder, opts *Options) {
		rec.failures = 99 // 全部 spawn 注入失败：视图停留 inline，无子进程
	})
	v := h.mgr.Acquire(h.opts)
	if mode := v.Mode(); mode != ModeInline {
		t.Fatalf("spawn 全败视图 Mode() = %q, want %q", mode, ModeInline)
	}
	if err := v.Close(); err != nil {
		t.Fatalf("Close err = %v, want nil", err)
	}
	if _, err := v.target(); !errors.Is(err, errClientClosed) {
		t.Fatalf("已关闭视图 target() err = %v, want errClientClosed", err)
	}
}
