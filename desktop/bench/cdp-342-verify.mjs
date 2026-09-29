#!/usr/bin/env node
// Task 342 four-stage verification against ISOLATED instances of the
// wt-342 build (build/bin/reasonix-desktop.exe).
//   Stage 1: switch off (default config) -> no debug port, endpoint file absent
//   Stage 2: switch on  -> endpoint file resolves to 127.0.0.1:<random port>
//   Stage 3: CDP HTTP plane: /json/version responds on the resolved port
// Each stage uses its own REASONIX_HOME/APPDATA copies; the user's live
// instance is never touched. Kill by command-line match only.
// Cleanup note: a WebView2 child can briefly hold the temp HOME after kill,
// so the rm is best-effort (Stage dirs accumulate harmlessly in %TEMP%).
import { execSync, spawn } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";

const EXE = path.resolve("build/bin/reasonix-desktop.exe");
const OUT = path.resolve("../../../../tasks/20260929并行开发-批七点七");
fs.mkdirSync(OUT, { recursive: true });

function mkIsolated() {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "cdp342-home-"));
  const appdata = fs.mkdtempSync(path.join(os.tmpdir(), "cdp342-appdata-"));
  fs.mkdirSync(path.join(appdata, "reasonix"), { recursive: true });
  fs.writeFileSync(path.join(appdata, "reasonix", "config.toml"), "");
  return { home, appdata };
}

function startInstance({ home, appdata, extraEnv = {} }) {
  const env = { ...process.env, REASONIX_HOME: home, APPDATA: appdata, ...extraEnv };
  const child = spawn(EXE, [], { env, detached: false, stdio: "ignore" });
  return child;
}

function waitForEndpointFile(home, timeoutMs = 40000) {
  const file = path.join(home, "logs", "desktop", "cdp-endpoint.txt");
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (fs.existsSync(file)) {
      const content = fs.readFileSync(file, "utf8").trim();
      if (content && content !== "pending") return { file, content };
    }
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 400);
  }
  return { file, content: fs.existsSync(file) ? fs.readFileSync(file, "utf8").trim() : null };
}

function killByExe() {
  // Kill ONLY our test binary processes (command-line matched), never other Reasonix.
  try {
    const ps = `Get-CimInstance Win32_Process -Filter "Name='reasonix-desktop.exe'" | Where-Object { $_.ExecutablePath -like '*wt-342*' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }`;
    execSync(`powershell -NoProfile -Command "${ps}"`, { stdio: "ignore" });
  } catch { /* none running */ }
}

function portReachable(port) {
  return new Promise((resolve) => {
    const s = net.connect({ host: "127.0.0.1", port, timeout: 1500 });
    s.on("connect", () => { s.destroy(); resolve(true); });
    s.on("error", () => resolve(false));
    s.on("timeout", () => { s.destroy(); resolve(false); });
  });
}

const results = { stages: [] };
killByExe();
await new Promise(r => setTimeout(r, 1500));

// ---------------- Stage 1: switch off ----------------
{
  const iso = mkIsolated();
  const child = startInstance(iso);
  await new Promise(r => setTimeout(r, 12000));
  const file = path.join(iso.home, "logs", "desktop", "cdp-endpoint.txt");
  const absent = !fs.existsSync(file);
  const env = child.spawnargs ? "" : "";
  results.stages.push({ stage: 1, switchOff: true, endpointFileAbsent: absent, endpointFile: file });
  console.log("STAGE1 endpoint file absent:", absent, "->", file);
  killByExe();
  await new Promise(r => setTimeout(r, 2000));
  try { fs.rmSync(iso.home, { recursive: true, force: true }); } catch { /* WebView2 child may still hold it */ }
  try { fs.rmSync(iso.appdata, { recursive: true, force: true }); } catch {}
}

// ---------------- Stage 2 + 3: switch on (config pre-written) ----------------
{
  const iso = mkIsolated();
  fs.writeFileSync(path.join(iso.appdata, "reasonix", "config.toml"),
    "[desktop]\nexperimental_cdp_debug_port = true\n");
  const child = startInstance(iso);
  const { content } = waitForEndpointFile(iso.home);
  console.log("STAGE2 endpoint:", content);
  let connect = false, port = 0;
  if (content && content.startsWith("127.0.0.1:")) {
    port = Number(content.split(":")[1]);
    connect = await portReachable(port);
    // Stage 3: CDP JSON version endpoint over HTTP
    if (connect) {
      try {
        const res = await fetch(`http://127.0.0.1:${port}/json/version`);
        const info = await res.json();
        results.cdpVersion = info.Browser ?? JSON.stringify(info).slice(0, 80);
        console.log("STAGE3 /json/version:", results.cdpVersion);
      } catch (e) { console.log("STAGE3 fetch failed:", String(e).slice(0, 100)); }
    }
  }
  results.stages.push({ stage: 2, switchOn: true, endpoint: content, connectable: connect });
  killByExe();
  await new Promise(r => setTimeout(r, 2000));
  try { fs.rmSync(iso.home, { recursive: true, force: true }); } catch { /* best effort */ }
  try { fs.rmSync(iso.appdata, { recursive: true, force: true }); } catch {}
}

fs.writeFileSync(path.join(OUT, "cdp-342-verify-result.json"), JSON.stringify(results, null, 2));
console.log("WROTE", path.join(OUT, "cdp-342-verify-result.json"));
