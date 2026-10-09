import { JSDOM } from "jsdom";
import {
  applySessionExperience,
  getSessionExperience,
  hydrateSessionExperience,
  resolveWorkProcessPresentation,
} from "../lib/sessionExperience";

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.localStorage = dom.window.localStorage;
globalThis.CustomEvent = dom.window.CustomEvent;

let passed = 0;
let failed = 0;
function check(value: boolean, message: string): void {
  if (value) {
    passed += 1;
    console.log(`  PASS  ${message}`);
  } else {
    failed += 1;
    console.log(`  FAIL  ${message}`);
  }
}

console.log("\nsession experience");
localStorage.clear();
localStorage.setItem("reasonix-session-experience", "deep");
// 任务 269 A3：水合前兼容镜像即权威（源码 getSessionExperience 注释原文），启动/
// 恢复窗口按用户真实档位渲染而不是假定 standard。旧断言「启动忽略镜像」是 A3
// 改语义时漏改的预存红（668 定向跑相关子集时发现，基线同红），对齐到既定语义。
check(getSessionExperience() === "deep", "startup honors the compatibility mirror before the backend snapshot");
hydrateSessionExperience("invalid");
check(getSessionExperience() === "standard", "invalid startup values normalize to standard");
check(resolveWorkProcessPresentation("standard").keepExpandedAfterCompletion === false, "standard collapses completed work");
check(resolveWorkProcessPresentation("deep").showWhileRunning === true, "deep shows work while running");
check(resolveWorkProcessPresentation("deep").keepExpandedAfterCompletion === true, "deep keeps completed work expanded");
// Task 111: concise never live-expands while the turn runs.
check(resolveWorkProcessPresentation("concise").showWhileRunning === false, "concise hides work while running");
check(resolveWorkProcessPresentation("concise").keepExpandedAfterCompletion === false, "concise collapses completed work");

applySessionExperience("deep");
check(getSessionExperience() === "deep", "apply persists deep");
check(localStorage.getItem("reasonix-session-experience") === "deep", "canonical localStorage key stores deep");
check(localStorage.getItem("reasonix-display-mode") === "standard", "compatibility density mirror stays standard");
check(localStorage.getItem("reasonix-process-fold") === "expanded", "deep mirrors the old expanded fold value");

applySessionExperience("concise");
check(getSessionExperience() === "concise", "apply persists concise");
check(localStorage.getItem("reasonix-session-experience") === "concise", "canonical localStorage key stores concise");
check(localStorage.getItem("reasonix-process-fold") === "auto", "concise mirrors auto fold (collapsed)");
hydrateSessionExperience("concise");
check(getSessionExperience() === "concise", "hydrate accepts concise from the backend");

// An authoritative startup snapshot must win over a stale local optimistic value.
hydrateSessionExperience("standard");
check(getSessionExperience() === "standard", "authoritative hydrate wins over stale localStorage");
check(localStorage.getItem("reasonix-session-experience") === "standard", "hydrate rewrites the canonical localStorage value");

if (failed > 0) {
  throw new Error(`${failed} session experience checks failed`);
}
console.log(`  ${passed} checks passed`);
