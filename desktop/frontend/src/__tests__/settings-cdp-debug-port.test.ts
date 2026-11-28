// Task 342 / tl1741x: the CDP debug port family pin — retirement shape.
//
// tl1741（用户 20261121 三拍）：stable 档不再提供实验开关，WebView2 CDP
// debug endpoint 随之无条件武装。本钉锁退役终态的每一个面：
//   - 实验室 rail 上 devDebug 卡保留（task 342 原始防丢教训的载体不动），
//     cdpDebugPort 是 stable 档位展示行，yaml/内置默认双布局源无退役键；
//   - labLightKeys 无谓词、pane 无开关接线、bridge/wailsjs 无 setter、
//     Go view 无字段、config render 表无渲染行、edit.go 无 setter、
//     两份 TS settings view 无死类型字段——撤开关=全链清干净；
//   - desktop/cdp_debug_port.go 正面锚：endpoint 无条件武装（loopback-only
//     随机端口安全姿态不变）；
//   - 存量手写 desktop.experimental_cdp_debug_port 键由 config deprecated
//     警告清单承接（tl1796 collab_hub 先例）。
//
// 本文件曾钉撤开关前的旧形（9 断言 7 红预存），tl1741x 随余项清理换代为
// 退役新形钉（52 套预存红甄别口径：本套红因=旧形断言，非行为回归）。
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

let passed = 0;
function ok(cond: boolean, label: string) {
  if (!cond) {
    process.stderr.write(`FAIL ${label}\n`);
    process.exitCode = 1;
    return;
  }
  passed += 1;
  process.stdout.write(`  PASS  ${label}\n`);
}

const here = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(here, "..", "..");
const repoRoot = path.resolve(frontendRoot, "..", "..");
// 任务724/1134 后实验室布局数据驱动:布局树在 src/lab/(yaml+内置默认双份),
// 卡内分支/setter 在 ExperimentalLabSection.tsx;SettingsPanel 只留挂载点。
const labLayout = fs.readFileSync(
  path.join(frontendRoot, "src/lab/labLayoutDefault.ts"),
  "utf8",
);
const labYaml = fs.readFileSync(
  path.join(frontendRoot, "src/lab/lab-layout.yaml"),
  "utf8",
);
const labLightKeys = fs.readFileSync(
  path.join(frontendRoot, "src/lab/labLightKeys.ts"),
  "utf8",
);
const labSection = fs.readFileSync(
  path.join(frontendRoot, "src/components/ExperimentalLabSection.tsx"),
  "utf8",
);
const view = fs.readFileSync(path.join(frontendRoot, "src/lib/settingsViewTypes.ts"), "utf8");
const typesTs = fs.readFileSync(path.join(frontendRoot, "src/lib/types.ts"), "utf8");
const bridge = fs.readFileSync(path.join(frontendRoot, "src/lib/bridge.ts"), "utf8");
const goBindings = fs.readFileSync(
  path.join(frontendRoot, "wailsjs/go/main/App.d.ts"),
  "utf8",
);
const settingsApp = fs.readFileSync(
  path.join(repoRoot, "desktop/settings_app.go"),
  "utf8",
);
const render = fs.readFileSync(
  path.join(repoRoot, "internal/config/render.go"),
  "utf8",
);
const editGo = fs.readFileSync(
  path.join(repoRoot, "internal/config/edit.go"),
  "utf8",
);
const cdpGo = fs.readFileSync(
  path.join(repoRoot, "desktop/cdp_debug_port.go"),
  "utf8",
);
const keyWarn = fs.readFileSync(
  path.join(repoRoot, "internal/config/config_key_warn.go"),
  "utf8",
);

// 1. Lab rail: the devDebug card stays (task 342's lost-save carrier), the
// cdpDebugPort row is a stable display member, and neither layout source
// carries the retired switch key.
ok(
  labLayout.includes('id: "devDebug"') &&
    labLayout.includes('key: "dev-debug"'),
  "lab rail hosts the devDebug card in the dev-debug group",
);
ok(
  labLayout.includes('id: "cdpDebugPort"') &&
    !labLayout.includes("experimentalCDPDebugPort"),
  "cdpDebugPort is a stable display row; default layout carries no retired key",
);
ok(
  labYaml.includes("cdpDebugPort") &&
    !labYaml.includes("experimentalCDPDebugPort") &&
    !labYaml.includes("experimental_cdp_debug_port"),
  "yaml layout source agrees (display row, no retired key)",
);

// 2. Light predicates: no labLightKeys entry for the retired switch.
ok(
  !labLightKeys.includes("experimentalCDPDebugPort: (s: SettingsView)"),
  "labLightKeys has no predicate for the retired switch",
);

// 3. Detail pane: renders, but wires no CDP toggle — the WebView2 environment
// is created once per process, so the endpoint cannot be toggled live and is
// armed unconditionally.
ok(
  labSection.includes('selected === "devDebug" && ('),
  "detail card renders for the devDebug family",
);
ok(
  !labSection.includes("SetExperimentalCDPDebugPort"),
  "pane wires no retired CDP switch call",
);

// 4. Bridge contract: the retired setter stays out of the bridge declarations
// (tl1842 cleared the dead declaration; this pin tracks the retirement) and
// out of the generated wailsjs bindings.
ok(
  !bridge.includes("SetExperimentalCDPDebugPort(enabled: boolean): Promise<void>;"),
  "retired SetExperimentalCDPDebugPort stays out of the bridge declarations",
);
ok(
  !goBindings.includes("SetExperimentalCDPDebugPort"),
  "wailsjs bindings carry no retired CDP setter",
);

// 5. Go chain: no view field, no render-table row, no config setter — the
// fixed-key-set save path has nothing to drop.
ok(
  !settingsApp.includes("ExperimentalCDPDebugPort bool"),
  "backend views carry no experimentalCDPDebugPort field",
);
ok(
  !render.includes("experimental_cdp_debug_port = %v"),
  "config render table lists no experimental_cdp_debug_port row",
);
ok(
  !editGo.includes("func (c *Config) SetExperimentalCDPDebugPort"),
  "config edit.go defines no SetExperimentalCDPDebugPort",
);

// 6. TS view types: neither SettingsView nor DesktopStartupSettingsView
// declares the dead field.
ok(
  !view.includes("experimentalCDPDebugPort?: boolean;") &&
    !typesTs.includes("experimentalCDPDebugPort?: boolean;"),
  "both TS settings views retired the field",
);

// 7. Endpoint arms unconditionally (positive anchor) — loopback-only random
// port posture unchanged (user ruling 2026-09-27 plan B).
ok(
  cdpGo.includes("--remote-debugging-port=0") &&
    cdpGo.includes("无条件武装"),
  "CDP endpoint arms unconditionally (loopback-only random port)",
);

// 8. Legacy handwritten keys: the deprecated warning list carries the retired
// key (WARN visible, non-blocking; fixed-key-set render clears the row on
// next save).
ok(
  keyWarn.includes('"desktop.experimental_cdp_debug_port"'),
  "deprecated-key list carries desktop.experimental_cdp_debug_port",
);

process.stdout.write(`\n${passed} passed\n`);
if (process.exitCode) process.exit(1);
