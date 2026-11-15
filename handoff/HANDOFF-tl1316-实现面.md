# wt-tl1316 实现面 handoff（宿主重启前保存 2026-11-01）

## 已完成（全部已 commit，工作区干净）
- `5b0ae691e` 设计文档（已验收）；分支已 rebase 到 main-v2-stable@22cd770d6。
- C1+C2 `f8d58fc68` 后继（实际 sha 见 git log）：runs 侧车+节点终态 checkpoint+ResumeWithID/workflow_run resume_run_id+interrupted 态+启动恢复+prune(100)+boot 回退源 + persist_resume_test.go 十二则（workflow 包全绿）。
- C3 `2f33bd856` 面板徽章/续跑钮/wire resumable/i18n 三语 + live-feed 测试 20 断言（66/66 绿）；`ba20ce6ef` service_test 签名适配。
- 已验证：go build ./... 绿；internal/workflow、internal/tool/builtin、internal/boot、desktop 主包测试绿；FE tsc --noEmit 绿；三个 workflow 面板套件（66/94/42）全绿。

## 下一步（C4 收尾）
1. `desktop/frontend` 跑 `node scripts/run-tests.mjs --keep-going` 全量（重启前被宿主终止，未跑完；定向三套件已绿）。
2. go 全量回归：`go build ./... && go test ./internal/... ./desktop/...`（根模块）+ `cd desktop && go test ./...`。
3. 十四则矩阵读数核对（设计文档 §8；Go 侧已覆盖 12 则，矩阵13 面板渲染由 FE 源码钉+套件覆盖、矩阵14 已在 TestServiceRestoreAndResumeInterruptedRun）。
4. 交付报告（wt/commit 分标①~⑥/矩阵读数/write_paths/撞面/预存红/通俗版）→ 不写 tl（职责在协调线），走 to-main 回板。

## 坑
- FE 测试必须 `node node_modules/tsx/dist/cli.mjs --tsconfig tsconfig.tsx-runner.json <file>`（.bin/tsx 是 sh wrapper，node 直跑会炸）；全量 runner 用 `node scripts/run-tests.mjs`。
- worktree 缺 wailsjs（gitignored 生成物）：已从主仓 reasonix/desktop/frontend/wailsjs 拷贝；tsc 需它。
- go.mod 根模块不含 desktop（desktop 有独立 go.mod，构建进 desktop 目录）。
- workflow 包测试里 saveRunSnapshot 的第一参是 **runs 目录**（runsDir(dir)），不是 WorkflowsDir。
- main-v2-stable 若又前进：先 rebase；1325 的 G1/G2 已在我基线内，无冲突史。
