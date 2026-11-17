// Run: tsx src/__tests__/tl1653-rewrite-refresh-contract.test.ts
//
// tl1653 刷新链契约钉（source-contract 判例，同 rewind-fork-routing /
// history-auto-retry-contract）：后端改写信号 → 前端整页重载的链条三环
// 逐环在册，缺一环即红。
//   环① Go：Diet 改写完成发 session_changed+SessionReset（钉在 Go 侧
//     tl1653_rewrite_refresh_nail_test.go，此处钉前端消费端不回退）。
//   环② FE：session_changed+sessionReset → loadSessionDataForTab 整页重载
//     （session-changed 面，head 切换分支的既有契约）。
//   环③ FE：runtime:rebuilt 处理器在 controller_rebuilt 后拉新 meta
//     （invalidate 只丢缓存不重取——20261116 事故的指纹漂移来源之一）。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const controller = readFileSync(join(root, "lib/useController.ts"), "utf8");

// 环②：session_changed+sessionReset 必须走整页重载（reset=true，
// reason=session-changed），不许退化为 patch。
// tl1653x 修订：重载面加指纹门（sessionChangedReloadNeeded）——同指纹二批
// 去重（resume 后同内容渲染两次的形态①修），指纹翻动/未知仍整页重载，
// 改写信号的消费能力（环②本体）不变。
assert.match(
  controller,
  /e\.kind === "session_changed" && e\.sessionReset[\s\S]{0,600}loadSessionDataForTab\(targetTabId, true, "session-changed"\)/,
  "环②：session_changed+sessionReset 的整页重载面缺失（Diet 改写信号将无人消费）",
);
assert.match(
  controller,
  /sessionChangedReloadNeeded\(statesRef\.current\.get\(targetTabId\), fresh\)[\s\S]{0,120}loadSessionDataForTab\(targetTabId, true, "session-changed"\)/,
  "环② tl1653x：session-changed 重载缺指纹门（二批去重面缺失，resume 后同内容重灌回归）",
);

// 环③：onRuntimeRebuilt 处理器在 controller_rebuilt 之后拉新 meta——
// 两个分支（指定 tab / 全局重建）都要有。
const rebuiltBlock = controller.slice(controller.indexOf("const offRebuilt = onRuntimeRebuilt"), controller.indexOf("const offEffortApplying"));
assert.ok(rebuiltBlock.length > 0, "onRuntimeRebuilt handler not found");
assert.match(rebuiltBlock, /dispatchTo\(rebuiltTabId, \{ type: "controller_rebuilt" \}\)/, "环③：指定 tab 分支缺 controller_rebuilt");
assert.match(rebuiltBlock, /void refreshMetaForTab\(rebuiltTabId\)/, "环③：指定 tab 分支缺 refreshMetaForTab（tl1653 修复回退）");
assert.match(rebuiltBlock, /void refreshMetaForTab\(id\)/, "环③：全局重建分支缺 refreshMetaForTab");

// 环②前置：reset 面在 deferResetUntilHistory 下原子换底（旧表面保持到新页
// 落地），不吃空白中间态——loadSessionDataForTab 的 defer 语义仍在。
assert.match(
  controller,
  /deferResetUntilHistory[^;]*options\.deferResetUntilHistory/,
  "reset 延后到历史落地（deferResetUntilHistory）语义缺失——换底会出现空白中间态",
);

console.log("tl1653 rewrite-refresh contract: all asserts passed");
