// Task 151 (round 3) — "switching between two large resident tabs" render bench.
//
// The data layer is already fixed (desktop.log shows switch-tab total=0ms and a
// local-snapshot history skip), yet a user-visible pause remains. The remaining
// suspect is the render layer: App.tsx held ONE Transcript instance, so a switch
// replaced the whole item set and React rebuilt the incoming tab's DOM. This fixture
// measures both shapes on the real component:
//
//   mode="swap"     — one Transcript instance, props replaced on switch (= before)
//   mode="resident" — the shipped TranscriptPaneResidency, one pane per tab (= after)
//
// Everything else (window projection, geometry measurement, markdown, stylesheet,
// the residency component itself) is the production code path.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { TranscriptPaneResidency } from "../src/app-shell/TranscriptPaneResidency";
import { Transcript } from "../src/components/Transcript";
import { app } from "../src/lib/bridge";
import { type Item } from "../src/lib/useController";
import { LocaleProvider } from "../src/lib/i18n";
import "../src/styles.css";

type Mode = "swap" | "resident";

type Options = {
  mode: Mode;
  turns: number;
  tabIds: string[];
};

type SwitchSample = {
  from: string;
  to: string;
  tabId: string;
  /** click -> second rAF after commit (user-visible pause). */
  total: number;
  /** click -> layout effect (React render + DOM mutation done, before paint). */
  commit: number;
  /** Nodes under the fixture root after the switch. */
  domNodes: number;
  /** Transcript rows currently materialised (bounded window). */
  rows: number;
  /** Windows only: JS heap in MB, best effort. */
  heapMb: number;
};

declare global {
  interface Window {
    tabRenderFixture: {
      configure: (next: Partial<Options>) => void;
      revision: number;
      switchTo: (tabId: string) => Promise<SwitchSample>;
      samples: SwitchSample[];
      mountedTabIds: () => string[];
    };
  }
}

// Only the backend boundary is stubbed, the same way bench/turn-result.tsx does it:
// bindings the transcript can reach fall through to their own bridge implementation,
// anything else resolves to undefined instead of throwing.
const bridgeFallback: Record<string, unknown> = {};
for (const key of ["ReportFrontendDiagnostic", "ReportFrontendLog", "ToolResultForTab", "ListDirForTab", "WorkspaceChanges", "GitBranches"]) {
  bridgeFallback[key] = (app as unknown as Record<string, unknown>)[key];
}
window.go = {
  main: {
    App: new Proxy(bridgeFallback, {
      get: (target, key) => target[key as string] ?? (async () => undefined),
    }),
  },
} as unknown as typeof window.go;

const noPrompt = () => {};

const MESSAGE_BODY = [
  "切换标签页时要看的不是数据层,而是渲染层:一个 Transcript 实例在切换时整组替换 items,",
  "React 于是要把新标签页的窗口重新建起来。下面这段代码块和上面的中文一样,只是用来把",
  "单个助手回合撑到接近真实大会话的体量。",
  "",
  "```ts",
  "export function measureSwitch(tabId: string, startedAt: number) {",
  "  return requestAnimationFrame(() => requestAnimationFrame(() => ({",
  "    tabId,",
  "    ms: performance.now() - startedAt,",
  "  })));",
  "}",
  "```",
].join("\n");

function buildItems(tabId: string, turns: number): Item[] {
  const items: Item[] = [];
  for (let index = 0; index < turns; index += 1) {
    items.push({
      kind: "user", id: `${tabId}-user-${index}`, historyTurn: index + 1,
      text: `第 ${index + 1} 轮:${tabId} 的提问内容,带上一点中文与标点,长度接近真实提问。`,
    });
    items.push({
      kind: "tool", id: `${tabId}-tool-${index}`, name: index % 3 === 0 ? "bash" : "read_file",
      status: "done", readOnly: index % 3 !== 0,
      args: JSON.stringify({ path: `src/lib/useController.ts`, offset: index * 10 }),
      output: `line ${index}\n`.repeat(12),
      durationMs: 120,
    });
    items.push({
      kind: "assistant", id: `${tabId}-answer-${index}`, streaming: false,
      text: `${MESSAGE_BODY}\n\n回合 ${index + 1} 的结论见上。`, reasoning: "",
    });
  }
  return items;
}

const now = () => performance.now();

function Fixture() {
  const [options, setOptions] = useState<Options>({ mode: "swap", turns: 240, tabIds: ["tab-a", "tab-b"] });
  const [revision, setRevision] = useState(0);
  const [active, setActive] = useState(options.tabIds[0]);
  const [resident, setResident] = useState<string[]>([options.tabIds[0]]);
  const pending = useRef<{ tabId: string; from: string; at: number; commit?: number; resolve: (sample: SwitchSample) => void } | null>(null);
  const samples = useRef<SwitchSample[]>([]);
  const activeRef = useRef(active);
  activeRef.current = active;
  // Per-tab item sets are stable across switches, exactly like the per-tab
  // controller state the app keeps resident.
  const itemsByTab = useMemo(() => {
    const map = new Map<string, Item[]>();
    for (const tabId of options.tabIds) map.set(tabId, buildItems(tabId, options.turns));
    return map;
  }, [options.tabIds, options.turns]);

  const switchTo = useMemo(() => (tabId: string) => new Promise<SwitchSample>((resolve) => {
    const from = activeRef.current;
    pending.current = { tabId, from, at: now(), resolve };
    activeRef.current = tabId;
    setActive(tabId);
    setResident((current) => (current.includes(tabId) ? current : [...current, tabId]));
  }), []);

  useLayoutEffect(() => {
    const entry = pending.current;
    if (entry && entry.commit === undefined) entry.commit = now() - entry.at;
  });
  useEffect(() => {
    const entry = pending.current;
    if (!entry) return;
    requestAnimationFrame(() => requestAnimationFrame(() => {
      if (pending.current !== entry) return;
      pending.current = null;
      const heap = (performance as Performance & { memory?: { usedJSHeapSize: number } }).memory;
      const sample: SwitchSample = {
        from: entry.from, to: entry.tabId, tabId: entry.tabId,
        total: now() - entry.at,
        commit: entry.commit ?? -1,
        domNodes: document.querySelectorAll("*").length,
        rows: document.querySelectorAll(".transcript__row").length,
        heapMb: heap ? Math.round(heap.usedJSHeapSize / 1048576) : -1,
      };
      samples.current.push(sample);
      entry.resolve(sample);
    }));
  });

  useLayoutEffect(() => {
    document.documentElement.dataset.themeStyle = "graphite";
    document.documentElement.dataset.theme = "light";
    document.documentElement.dataset.platform = "windows";
    window.tabRenderFixture = {
      configure: (next) => {
        setOptions((current) => ({ ...current, ...next }));
        if (next.tabIds) {
          setResident([next.tabIds[0]]);
          setActive(next.tabIds[0]);
        }
        setRevision((value) => value + 1);
      },
      revision,
      switchTo,
      samples: samples.current,
      mountedTabIds: () => [...document.querySelectorAll("[data-transcript-pane]")].map((node) => node.getAttribute("data-transcript-pane") ?? ""),
    };
  }, [revision, switchTo]);

  // The residency mechanism ships with its own stylesheet, and that stylesheet is
  // reverted together with the mechanism. The fixture therefore keeps the hidden-pane
  // geometry itself — otherwise the "resident" mode would lay two visible transcripts
  // out next to each other and the comparison would be meaningless.
  useLayoutEffect(() => {
    const styleId = "tab-render-resident-style";
    if (document.getElementById(styleId)) return;
    const style = document.createElement("style");
    style.id = styleId;
    style.textContent = [
      ".transcript-split__pane{position:relative}",
      ".transcript-pane--resident{position:absolute;inset:0;overflow:hidden;visibility:hidden;pointer-events:none}",
    ].join("");
    document.head.appendChild(style);
  }, []);

  const renderTranscript = (tabId: string) => (
    <Transcript
      items={itemsByTab.get(tabId) ?? []}
      tabId={tabId}
      geometrySessionKey={`tab:${tabId}`}
      footerHeight={72}
      onPrompt={noPrompt}
    />
  );

  return (
    <div className="app app--windows app--windows-frameless app--creation" data-fixture-revision={revision}>
      <div className="layout">
        <header className="topicbar">Tab switch render fixture</header>
        <aside className="sidebar">Sidebar</aside>
        <div className="chat-pane">
          <main className="main">
            <div className="transcript-navigation-surface">
              <div className="transcript-navigation-content">
                <div className="transcript-split">
                  <div className="transcript-split__pane">
                    {options.mode === "resident"
                      ? <TranscriptPaneResidency panes={resident} visibleTabId={active}
                          renderPane={(tabId) => renderTranscript(tabId)} />
                      : renderTranscript(active)}
                  </div>
                </div>
              </div>
            </div>
          </main>
          <footer className="footer"><div className="composer-wrap"><textarea aria-label="Draft" defaultValue="Draft" /></div></footer>
        </div>
      </div>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(<LocaleProvider><Fixture /></LocaleProvider>);
