// Run: tsx src/__tests__/collab-contact-names.test.tsx
// 任务462: 跨会话消息卡显示双方对话名 ——
// ① 名单里查得到的 contact_id 渲染会话名（双方）；
// ② 完整 contact_id 不丢失（hover/title 属性可见）；
// ③ 改名后（名单数据变化 + 强制刷新）名字同步到新值；
// ④ 查不到名字降级为截短 id，绝不空白；
// ⑤ 非 collab 来源（IM 卡）的 meta 保持原样。
//
// 名单接口口径（调研结论，见交付报告）：ListAddressableSessions 一轮返回
// 全量通讯录（全局/项目/已归档），等价于按 contact_id 批量解析；名字在
// 查询时点现读，所以「改名后同步」收敛于 TTL 窗口/聚焦/面板打开。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { UserMessage } from "../components/Message";
import { LocaleProvider } from "../lib/i18n";
import {
  collabDisplayLabel,
  refreshCollabContactNames,
  rememberCollabContactNames,
  resetCollabContactNamesForTest,
  setCollabContactDirectory,
  shortContactId,
  useCollabContactNames,
} from "../lib/collabContactNames";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function flushTimers(ms = 0): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeEventListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

// 仿真 contact_id：与后端 newContactID() 同构 —— sc_ + 24 位 hex，共 27 位。
const FROM_ID = "sc_aaaa1111bbbb2222cccc3333";
const TO_ID = "sc_dddd4444eeee5555ffff6666";

function collabText(fromId: string, toId: string): string {
  return `[跨会话消息] 来自 contact_id=${fromId} → 发至 contact_id=${toId} (hop=1)\n\n请调研收件箱的渲染面。`;
}

type DirectoryRow = { contactId: string; title?: string };

function makeDirectory(rows: DirectoryRow[], calls: number[] = []) {
  return {
    async ListAddressableSessions(): Promise<DirectoryRow[]> {
      calls.push(calls.length + 1);
      return rows.map((row) => ({ ...row }));
    },
  };
}

async function main() {
  // ── 纯函数：降级短 id ────────────────────────────────────────────
  {
    const long = shortContactId(FROM_ID);
    ok(long.length < FROM_ID.length && long.includes("…"), `long contact_id truncates with ellipsis (${long})`);
    ok(long.startsWith("sc_") && long.endsWith("3333"), "truncated id keeps head and tail for recognition");
    eq(shortContactId("sc_short"), "sc_short", "short contact_id renders as-is");
    eq(shortContactId("   "), "(未知会话)", "empty contact_id degrades to a placeholder, never blank");
  }

  // ── 名单解析：有名用名 / 无名降级 / 改名同步 ─────────────────────
  {
    resetCollabContactNamesForTest();
    const calls: number[] = [];
    const rows: DirectoryRow[] = [
      { contactId: FROM_ID, title: "调研主对话" },
      { contactId: TO_ID, title: "开发子对话" },
    ];
    setCollabContactDirectory(makeDirectory(rows, calls));
    await act(async () => {
      await refreshCollabContactNames({ force: true });
    });
    eq(calls.length, 1, "a forced refresh queries the directory once");
    eq(collabDisplayLabel(FROM_ID), "调研主对话", "a listed contact_id resolves to the session name");
    eq(collabDisplayLabel(TO_ID), "开发子对话", "the recipient resolves to its session name too");
    const unknown = collabDisplayLabel("sc_ffff00001111222233334444");
    ok(unknown.includes("…") && unknown.includes("sc_"), `an unlisted contact_id degrades to the truncated id (${unknown})`);
    ok(unknown.length > 0 && !unknown.includes("undefined"), "the degraded label is never blank/undefined");

    // ③ 改名后同步：名单数据更新 + 强制刷新 → 显示新名。
    rows[0] = { contactId: FROM_ID, title: "改名后的调研对话" };
    await act(async () => {
      await refreshCollabContactNames({ force: true });
    });
    eq(collabDisplayLabel(FROM_ID), "改名后的调研对话", "after a rename + refresh the label follows the new title");

    // rememberCollabContactNames：面板打开时喂名单也能即时生效。
    resetCollabContactNamesForTest();
    act(() => {
      rememberCollabContactNames([{ contactId: TO_ID, title: "喂进来的名字" }]);
    });
    eq(collabDisplayLabel(TO_ID), "喂进来的名字", "panel-fed roster rows resolve immediately");
    eq(collabDisplayLabel(""), "(未知会话)", "empty id still degrades after cache fills");
    setCollabContactDirectory(null);
    resetCollabContactNamesForTest();
  }

  // ── 消息卡：有名 → 双方会话名；hover 保留完整 id ────────────────
  {
    resetCollabContactNamesForTest();
    const dom = installDom();
    setCollabContactDirectory(makeDirectory([
      { contactId: FROM_ID, title: "调研主对话" },
      { contactId: TO_ID, title: "开发子对话" },
    ]));
    const root = createRoot(document.getElementById("root")!);
    await act(async () => {
      root.render(
        <LocaleProvider>
          <UserMessage text={collabText(FROM_ID, TO_ID)} />
        </LocaleProvider>,
      );
      await flushTimers();
    });
    const card = document.querySelector(".im-source-card");
    ok(Boolean(card), "the collab message renders the im-source card");
    const text = document.body.textContent ?? "";
    ok(text.includes("调研主对话"), "the sender's session name renders on the card");
    ok(text.includes("开发子对话"), "the recipient's session name renders on the card");
    ok(!text.includes(FROM_ID) && !text.includes(TO_ID), "raw contact_ids stay out of the visible text (names replace them)");
    const meta = card!.querySelector(".im-source-card__meta");
    ok(Boolean(meta), "the meta line renders");
    ok(
      (meta!.textContent ?? "").includes("调研主对话") && (meta!.textContent ?? "").includes("开发子对话"),
      "the meta line shows both conversation names",
    );
    const hover = meta!.getAttribute("title") ?? "";
    ok(hover.includes(`contact_id=${FROM_ID}`), `hover keeps the full sender contact_id (${hover})`);
    ok(hover.includes(`contact_id=${TO_ID}`), "hover keeps the full recipient contact_id");
    await act(async () => {
      root.unmount();
    });
    setCollabContactDirectory(null);
    resetCollabContactNamesForTest();
    dom.window.close();
  }

  // ── 消息卡：无名 → 降级截短 id（非空白），hover 仍有完整 id ─────
  {
    resetCollabContactNamesForTest();
    const dom = installDom();
    setCollabContactDirectory(makeDirectory([])); // 名单为空 = 两边都查不到名
    const root = createRoot(document.getElementById("root")!);
    await act(async () => {
      root.render(
        <LocaleProvider>
          <UserMessage text={collabText(FROM_ID, TO_ID)} />
        </LocaleProvider>,
      );
      await flushTimers();
    });
    const card = document.querySelector(".im-source-card");
    ok(Boolean(card), "the degraded card still renders");
    const meta = card!.querySelector(".im-source-card__meta");
    const metaText = meta?.textContent ?? "";
    ok(metaText.trim().length > 0, "the degraded meta line is not blank");
    ok(
      metaText.includes(shortContactId(FROM_ID)) && metaText.includes(shortContactId(TO_ID)),
      `both sides degrade to their truncated ids (${metaText.trim()})`,
    );
    const hover = meta!.getAttribute("title") ?? "";
    ok(hover.includes(`contact_id=${FROM_ID}`) && hover.includes(`contact_id=${TO_ID}`), "the degraded card's hover still carries the full ids");
    await act(async () => {
      root.unmount();
    });
    setCollabContactDirectory(null);
    resetCollabContactNamesForTest();
    dom.window.close();
  }

  // ── 非 collab 来源（IM 卡）meta 保持原样 ────────────────────────
  {
    resetCollabContactNamesForTest();
    const dom = installDom();
    const imText = [
      "[[reasonix-im]]",
      "provider=lark",
      "label=Lark 通知",
      "sender=u_1001",
      "chat=group_dev",
      "[[/reasonix-im]]",
      "群里提到你",
    ].join("\n");
    const root = createRoot(document.getElementById("root")!);
    await act(async () => {
      root.render(
        <LocaleProvider>
          <UserMessage text={imText} />
        </LocaleProvider>,
      );
      await flushTimers();
    });
    const card = document.querySelector(".im-source-card");
    ok(Boolean(card), "the IM card renders");
    const meta = card!.querySelector(".im-source-card__meta");
    ok((meta?.textContent ?? "").includes("u_1001"), "IM sender keeps the plain sender display");
    eq(meta!.getAttribute("title") ?? "", "", "the IM meta carries no collab hover payload");
    await act(async () => {
      root.unmount();
    });
    setCollabContactDirectory(null);
    resetCollabContactNamesForTest();
    dom.window.close();
  }

  // ── hook 订阅：缓存更新 → 已挂载组件就地改名 ────────────────────
  {
    resetCollabContactNamesForTest();
    const dom = installDom();
    setCollabContactDirectory(makeDirectory([{ contactId: FROM_ID, title: "旧名" }]));
    let seenLabel = "";
    function Probe() {
      const names = useCollabContactNames();
      seenLabel = names.get(FROM_ID) ?? "";
      return null;
    }
    const root = createRoot(document.getElementById("root")!);
    await act(async () => {
      root.render(<Probe />);
      await flushTimers();
    });
    eq(seenLabel, "旧名", "the mounted hook sees the roster title on mount");

    // 改名：换名单 + 面板喂新数据（remember 通知订阅者）→ 就地更新。
    setCollabContactDirectory(makeDirectory([{ contactId: FROM_ID, title: "新名" }]));
    await act(async () => {
      rememberCollabContactNames([{ contactId: FROM_ID, title: "新名" }]);
      await flushTimers();
    });
    eq(seenLabel, "新名", "a mounted hook updates in place after a rename reaches the cache");
    await act(async () => {
      await refreshCollabContactNames({ force: true });
    });
    eq(seenLabel, "新名", "a forced refresh against the renamed roster keeps the new title");
    await act(async () => {
      root.unmount();
    });
    setCollabContactDirectory(null);
    resetCollabContactNamesForTest();
    dom.window.close();
  }

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

main().catch((err) => {
  process.stderr.write(`${err?.stack ?? err}\n`);
  process.exit(1);
});
