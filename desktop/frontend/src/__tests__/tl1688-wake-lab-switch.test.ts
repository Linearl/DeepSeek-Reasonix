// tl1688：唤醒开关实验室 UI（放「智能自动化」下面）。
//
// 钉住四件事：①config setter 写盘且默认关不变 ②desktop 桥方法与快照字段就位
// ③前端开关在实验室「智能自动化」组内、默认 false、切换走 config 桥
// ④三语键齐备且含额度影响说明（3 轮/600s + 60s 节流）。
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import assert from "node:assert/strict";

const testDir = dirname(fileURLToPath(import.meta.url));
const read = (rel: string) => readFileSync(resolve(testDir, rel), "utf8");

const REPO = resolve(testDir, "../../../..");

test("tl1688: config setter 写盘且默认关不变", () => {
  const src = readFileSync(resolve(REPO, "internal/config/edit.go"), "utf8");
  assert.match(src, /func \(c \*Config\) SetBackgroundJobWakeIdleSession\(enabled bool\) error/, "缺 config setter");
  // 默认仍为关（指针 nil 即关）——setter 不得把 nil 当成 true。
  const configSrc = readFileSync(resolve(REPO, "internal/config/config.go"), "utf8");
  assert.match(
    configSrc,
    /func \(c \*Config\) BackgroundJobWakeIdleSession\(\) bool \{\s*return c\.Tools\.BackgroundJobs\.WakeIdleSession != nil && \*c\.Tools\.BackgroundJobs\.WakeIdleSession\s*\}/,
    "默认判定被改动（必须 nil 即关）",
  );
});

test("tl1688: desktop 桥方法与快照字段就位", () => {
  const desktop = readFileSync(resolve(REPO, "desktop/settings_preferences.go"), "utf8");
  assert.match(desktop, /func \(a \*App\) SetBackgroundJobWakeIdleSession\(enabled bool\) error/, "缺 desktop 桥方法");
  const snap = readFileSync(resolve(REPO, "desktop/settings_app.go"), "utf8");
  assert.match(snap, /BackgroundJobWakeIdleSession bool `json:"backgroundJobWakeIdleSession"`/, "缺快照字段");
  assert.match(snap, /BackgroundJobWakeIdleSession: cfg\.BackgroundJobWakeIdleSession\(\)/, "快照未读配置");
});

test("tl1688: 前端开关在「智能自动化」组内且走 config 桥", () => {
  const comp = read("../components/ExperimentalLabSection.tsx");
  // 位置：必须紧跟「智能自动化」header 字段之后（用户指定位置）。
  const headerIdx = comp.indexOf('labLabel("smartAutomation"');
  const switchIdx = comp.indexOf("settings.backgroundJobWakeIdleSession");
  assert.ok(headerIdx > 0 && switchIdx > headerIdx, "开关未位于智能自动化组之后");
  assert.match(comp, /app\.SetBackgroundJobWakeIdleSession\(on\)/, "开关未走 config 桥");
  assert.match(comp, /Boolean\(s\.backgroundJobWakeIdleSession\) === on/, "开关未读快照值");
});

test("tl1688: bridge 类型与 mock 齐备", () => {
  const bridge = read("../lib/bridge.ts");
  assert.match(bridge, /SetBackgroundJobWakeIdleSession\(enabled: boolean\): Promise<void>/, "缺桥类型声明");
  assert.match(bridge, /async SetBackgroundJobWakeIdleSession\(\) \{\}/, "缺 mock 实现");
  const types = read("../lib/types.ts");
  assert.match(types, /backgroundJobWakeIdleSession\?: boolean/, "缺快照类型字段");
});

test("tl1688: 三语键齐备且含额度影响说明", () => {
  for (const locale of ["zh.ts", "en.ts", "zh-TW.ts"]) {
    const src = read(`../locales/${locale}`);
    for (const key of [
      "settings.backgroundJobWakeIdleSession",
      "settings.backgroundJobWakeIdleSessionHint",
      "settings.backgroundJobWakeIdleSession.on",
      "settings.backgroundJobWakeIdleSession.off",
    ]) {
      // tl1857 (CodeQL js/incomplete-sanitization #335)：键为固定字面量（仅字母
      // 与点号），无需构造正则——改用子串断言，语义等价且不再有不完整转义。
      assert.ok(src.includes(`"${key}"`), `${locale} 缺 ${key}`);
    }
    // 额度影响说明必须写明自驱预算（轮数/窗口/间隔）。
    const hint = src.match(/"settings\.backgroundJobWakeIdleSessionHint":\s*"([^"]*)"/);
    assert.ok(hint, `${locale} 缺说明文案`);
    assert.match(hint[1], /3|600|60/, `${locale} 说明未含额度影响（3 轮/600s/60s）`);
  }
});
