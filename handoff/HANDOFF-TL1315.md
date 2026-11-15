# tl1315 现场交接（宿主重启前快照 2026-11-01）

## 已完成（3 commits 在 wt-tl1315 分支，基线 main-v2-stable@bd8cbb497）
1. `243de0756` fix(tl1315)① recovery_gc.go：LoadSessionContentSnapshot 判定式失败（normalizedDirty/eventLogDamaged）按「内容身份+转录/事件日志 size:mtime」记忆（F3 根治，探针实测重复扫描 3.1s→0.09s）；测试 tl1315_content_snapshot_refusal_test.go 五例+变异验证。
2. `daf6b13c0` fix(tl1315)② reconcile_status.go failDirectoryScan WARN/INFO 留痕 + reconcile_queue.go 慢扫描（≥30s）INFO、2min 预算具名常量。
3. `WIP` fix(tl1315)③ 前端：横幅冻结根治+失败留痕（sessionCatalogPresentation.ts/ProjectTree.tsx/三语 locale/测试 7 断言）。已验证：3 个 catalog 前端测试+tsc+eslint+comment-leak 绿。

## 根因结论（写报告用，全部已验证）
- 横幅唯一运行期来源=internal/sessioncatalog/reconcile_status.go failDirectoryScan（全局 degraded+LastError）；前端 failed 态渲染于 ProjectTree.tsx:3459。
- 触发机制：catalog 每 18s 左右扫一次 global-workspace sessions 目录（2885 文件/5GB/26 恢复副本全 diverged），逐个 LoadSessionContentSnapshot 判定谱系；011346 族等 normalizedDirty 会话判定恒失败且不缓存→每轮全量重解析（2-3.3s/个）→扫描 75-120s→偶发越 2min 预算（reconcile_queue.go runQueuedReconcile）→ctx.Err→failDirectoryScan→degraded→横幅；次日 DB 实测常驻 scanning idx=0/156-158。
- rebuild 失败：desktop/session_catalog_rebuild.go stopSessionCatalog(5s) 要求旧 catalog 关闭，但该目录扫描/入库事务不响应 ctx 取消（mutationMu 内）→5s 预算耗尽→errSessionCatalogStopTimeout；本地实测从未出现 v8.sqlite.rebuild.lock（未进 projectiondb.Rebuild 原子替换）。前端 catch 吞错+catalogRebuildFailedRef 冻结横幅=③修。
- 锁等待：扫描 LoadSession 持 save-path 锁（save.go:1824）与在写会话互抢；011346-7503ae1e 窗口 284 saves/持锁 690s、40+31+10 次 wait sum 188s（saver 26/92.5s+非 saver 46/80.5s）。
- d) pre-repair 备份 22 目录共约 2.3GB（清单在报告素材），删除候用户确认，本件只建议。
- e) 011346 写者=桌面主进程多控制器（save window 锁归因+fork 时间线 15:47→00:38 与 conflicts.jsonl 对齐），非外部进程；save-without-lease 全部 sa_* 自动会话 writer=thinkbook14_YJC 同机。
- 上游对照：upstream main-v2 的 MetadataOnly 拆分（#11513/#12822，扫描不读内容+ProveRecoveryLineage 后置）正是本根因的机制级解法，fork 未含——列为中期候选（面大不硬修）。

## 下一步（重启后）
1. 确认 3 commits 完整（git log --oneline -3）。
2. 重跑前端全量 suite：cd desktop/frontend && node scripts/run-tests.mjs --keep-going。
3. go test ./desktop/ ./internal/agent/ ./internal/sessioncatalog/（agent 全量有 3 个预存红：TestConcurrentLoadDuringAppendsStaysConsistent/TestListGroupRowsAndMemberTags/TestGuidanceSevenScenarios，已验与本次改动无关=基线同样红；sessioncatalog 有 3 个预存红同理性验证过）。
4. 写深查报告落 docs/issues/reports/调研报告【非诊断】/tl1315-项目树整理失败深查-20261101.md（在 global-workspace 仓库，参考 tl1083 报告格式；素材：tmp/tl1315-scratch/ 下探针数据、日志快照 logs/、DB 采样、family_attr 输出）。
5. 报告 commit 后交付：wt=wt-tl1315；commits=243de0756/daf6b13c0/WIP③；预存红申报；d/e 结论；撞面声明（已对照 tl1083 报告，本件只碰 catalog 扫描面+侧栏横幅面，与其投影回退面零交集）。
6. 清理：tmp/tl1315-scratch（含 DB 副本/日志快照/探针）、wt 内 desktop/frontend/node_modules+wailsjs 两个 junction、_recycle-20261101/wt-tl1315-node_modules-deepcopy-mistake。

## 坑
- git worktree 建法必须 cd 主仓 + ../worktrees/ 相对路径（已遵守）。
- frontend node_modules 用 junction（mklink /J，MSYS2_ARG_CONV_EXCL='*'）；第一次误 ln -s 变深拷贝 500MB+，已移 _recycle-20261101。
- wailsjs 也是生成目录，tsc 需要 junction 到主仓。
- Bash 无 iconv；写中文 commit 用文件+git commit -F；Python 打印需 PYTHONIOENCODING=utf-8。
- desktop.log 轮转快照在 tmp/tl1315-scratch/logs/（19:00-02:00 窗口）。
