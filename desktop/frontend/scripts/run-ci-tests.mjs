import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { closeSync, mkdtempSync, openSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { discoveredTests, testPlan } from "./ci-test-plan.mjs";

process.chdir(path.resolve(path.dirname(fileURLToPath(import.meta.url)), ".."));
const scripts = JSON.parse(readFileSync("package.json", "utf8")).scripts;
const plan = testPlan(scripts, discoveredTests());
if (process.argv.includes("--list")) {
  console.log(JSON.stringify(plan, null, 2));
} else {
  const concurrency = Number(process.env.REASONIX_TEST_CONCURRENCY ?? 2);
  if (!Number.isInteger(concurrency) || concurrency < 1 || concurrency > 4) throw new Error("test concurrency must be 1 to 4");
  // tl1788 pattern 1 triage switch: a failure under concurrency gets exactly
  // one serial re-run; green there means environment noise (ENV-FLAKY), a
  // second red is a real failure. Set REASONIX_TEST_REVERIFY=0 for the old
  // first-failure-is-final semantics.
  const reverify = process.env.REASONIX_TEST_REVERIFY !== "0";
  // tl1788 preflight: catch a broken pnpm resolution chain BEFORE scheduling,
  // so a poisoned environment aborts with one actionable error instead of
  // mass-red noise (tl1776: 230/287 suites red with ERR_MODULE_NOT_FOUND
  // pointing into the linked store while every suite passed when run alone).
  const require = createRequire(import.meta.url);
  try {
    require.resolve("jsdom");
  } catch {
    console.error("run-ci-tests: preflight failed — jsdom does not resolve from desktop/frontend/node_modules.");
    console.error("The pnpm link structure backing this checkout is broken; fix node_modules (reinstall deps in the real checkout this worktree links to) and re-run. Aborting instead of mass-red.");
    process.exit(1);
  }
  const commands = { tsx: require.resolve("tsx/cli"), tsc: require.resolve("typescript/bin/tsc") };
  const logs = mkdtempSync(path.join(tmpdir(), "reasonix-ci-tests-"));
  let failed = false;
  let completed = 0;
  const envFlaky = [];
  const children = new Set();
  for (const signal of ["SIGTERM", "SIGINT"]) process.on(signal, () => {
    failed = true;
    for (const child of children) child.kill(signal);
    process.exitCode = 1;
  });
  async function runOnce(entry) {
    const started = performance.now();
    const log = path.join(logs, `${plan.indexOf(entry)}.log`);
    const fd = openSync(log, "w");
    const [command, ...args] = entry.args;
    const result = await new Promise(resolve => {
      const child = spawn(process.execPath, command === "node" ? args : [commands[command], ...args], {
        stdio: ["ignore", fd, fd], timeout: 10 * 60 * 1000,
        env: { ...process.env, LANG: "en_US.UTF-8", LC_ALL: "en_US.UTF-8" },
      });
      children.add(child);
      child.once("error", error => resolve({ error }));
      child.once("close", (code, signal) => { children.delete(child); resolve({ code, signal }); });
    });
    closeSync(fd);
    return { result, log, durationMs: Math.round(performance.now() - started) };
  }
  function reportFailure(entry, attempt, confirmed) {
    failed = true;
    console.error(`${entry.key} FAILED (${attempt.durationMs}ms)${confirmed ? " — re-run confirmed red" : ""}`);
    console.error(attempt.result.error ?? attempt.result.signal ?? `exit ${attempt.result.code}`);
    console.error(readFileSync(attempt.log, "utf8"));
  }
  async function run(entry) {
    const first = await runOnce(entry);
    if (first.result.code === 0 && !first.result.error) {
      completed++;
      console.log(`PASS ${entry.key} (${first.durationMs}ms)`);
      return;
    }
    if (reverify) {
      console.log(`REVERIFY ${entry.key} (${first.durationMs}ms first attempt failed; re-running alone with identical argv)`);
      const second = await runOnce(entry);
      completed++;
      if (second.result.code === 0 && !second.result.error) {
        envFlaky.push(entry.key);
        // ENV-FLAKY is environment noise, not red: it does not block the run,
        // but every instance stays on the record below so dispatch can judge.
        console.log(`ENV-FLAKY ${entry.key} (green on serial re-run, ${second.durationMs}ms)`);
        return;
      }
      reportFailure(entry, second, true);
      return;
    }
    completed++;
    reportFailure(entry, first, false);
  }
  try {
    const queue = plan.filter(entry => !entry.serial);
    console.log(`CI: ${plan.length} unique commands; concurrency=${concurrency}; performance benchmark runs alone; re-verify=${reverify ? "on" : "off"}`);
    await Promise.all(Array.from({ length: concurrency }, async () => {
      while (queue.length && !failed) await run(queue.shift());
    }));
    for (const entry of plan.filter(entry => entry.serial)) if (!failed) await run(entry);
  } finally {
    if (envFlaky.length > 0) {
      console.log(`CI: ${envFlaky.length}/${plan.length} ENV-FLAKY (failed under concurrency, green on serial re-run — environment noise, NOT red):`);
      for (const key of envFlaky) console.log(`  ENV-FLAKY ${key}`);
    }
    rmSync(logs, { recursive: true, force: true });
  }
  console.log(`CI: ${completed}/${plan.length} commands completed`);
  if (failed || completed !== plan.length) process.exitCode = 1;
}
