// 任务 739：设置页打开钩子（store/appNavigation 的 setSettingsOpenHook）。
// 契约：openPage({kind:"settings"}) 在调用同步帧触发钩子且只触发一次；
// 非 settings 页不触发；setSettingsTarget 走 openPage 同样触发；可复位。
import assert from "node:assert/strict";
import { setSettingsOpenHook, useAppNavigationStore as store } from "../store/appNavigation";

const fired: string[] = [];
setSettingsOpenHook(() => { fired.push("open"); });

store.getState().openPage({ kind: "trash" });
assert.deepEqual(fired, [], "non-settings navigation must not fire the hook");

store.getState().openPage({ kind: "settings", tab: "general" });
assert.deepEqual(fired, ["open"], "openPage(settings) fires the hook");
assert.equal(store.getState().page.kind, "settings");

// 命令面板/侧栏按钮入口：setSettingsTarget 内部走 openPage，同样命中钩子
store.getState().setSettingsTarget("models");
assert.deepEqual(fired, ["open", "open"], "setSettingsTarget routes through openPage and fires the hook");

// 已在设置页时 setSettingsTarget 换 tab 也触发（同一同步帧发出预取无害，TTL 内去重）
store.getState().setSettingsTarget("appearance");
assert.deepEqual(fired, ["open", "open", "open"], "retargeting while on settings still routes through openPage");

// 复位后不再触发（App 卸载/测试隔离用）
setSettingsOpenHook(null);
store.getState().setSettingsTarget("models");
store.getState().openPage({ kind: "settings", tab: "general" });
assert.deepEqual(fired, ["open", "open", "open"], "null hook is a no-op");
store.getState().returnToWorkspace();

console.log("PASS settings open hook: settings-only, sync-frame, setSettingsTarget passthrough, resettable");
