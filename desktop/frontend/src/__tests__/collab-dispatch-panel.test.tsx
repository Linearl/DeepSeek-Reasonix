// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/collab-dispatch-panel.test.tsx
// tl1696：派发记录四态面板 ——
// ① 四态徽标逐行可见（等待/进行中/已回信/已拒绝），未知状态诚实缺席；
// ② 角色徽标按 toRole 渲染（main/sub），未知角色不渲染；
// ③ 已回信行带回信摘要；
// ④ 点击行直达目标会话（ActivateTopic 参数取自通讯录行：scope/workspaceRoot/topicId/sessionPath）；
// ⑤ 目录未命中 → 就地说明，绝不静默；
// ⑥ 空数据空态 / degraded 降级 / 读取失败显式报错+重试——三态分离，绝不冒充空态。

import { JSDOM } from "jsdom";

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

function flush(ms = 0): Promise<void> {
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
  return dom;
}

type Row = {
  id: string;
  fromContactId: string;
  toContactId: string;
  fromRole?: string;
  toRole?: string;
  toTitle?: string;
  preview: string;
  delivery?: string;
  requireReply?: boolean;
  at: number;
  status: string;
  statusAt?: number;
  statusDetail?: string;
  replyPreview?: string;
  replyAt?: number;
};

type Snapshot = {
  rows: Row[];
  total: number;
  returned: number;
  truncated?: boolean;
  degraded?: boolean;
};

type DirectoryRow = {
  contactId: string;
  title?: string;
  topicId?: string;
  sessionPath: string;
  scope?: string;
  workspaceRoot?: string;
  open: boolean;
  archived?: boolean;
};

type BindingCalls = {
  listSent: number;
  activate: Array<{ scope: string; workspaceRoot: string; topicId: string; sessionPath: string }>;
};

function makeBindings(opts: {
  snapshot?: Snapshot;
  reject?: boolean;
  roster?: DirectoryRow[];
}) {
  const calls: BindingCalls = { listSent: 0, activate: [] };
  const bindings = {
    ListCollabSentMail: async (_limit: number): Promise<Snapshot> => {
      calls.listSent += 1;
      if (opts.reject) throw new Error("gateway closed");
      return opts.snapshot ?? { rows: [], total: 0, returned: 0 };
    },
    ListAddressableSessions: async (): Promise<DirectoryRow[]> => opts.roster ?? [],
    ActivateTopic: async (scope: string, workspaceRoot: string, topicId: string, sessionPath: string) => {
      calls.activate.push({ scope, workspaceRoot, topicId, sessionPath });
      return {};
    },
  };
  return { bindings, calls };
}

const baseRow = (over: Partial<Row>): Row => ({
  id: "msg_x",
  fromContactId: "sc_main",
  toContactId: "sc_sub",
  preview: "dispatch body",
  at: 1700000000000,
  status: "waiting",
  ...over,
});

async function renderPanel(bindings: unknown) {
  const React = (await import("react")).default;
  const { act } = await import("react");
  const { createRoot } = await import("react-dom/client");
  const { LocaleProvider } = await import("../lib/i18n");
  const { CollabDispatchPanel } = await import("../components/CollabDispatchPanel");
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <CollabDispatchPanel bindings={bindings as never} />
      </LocaleProvider>,
    );
  });
  await act(async () => { await flush(20); });
  return { React, act, root };
}

function text() {
  return document.body.textContent ?? "";
}

function q(sel: string): Element[] {
  return Array.from(document.querySelectorAll(sel));
}

async function testFourStatesRender() {
  process.stdout.write("① 四态徽标逐行可见\n");
  const snap: Snapshot = {
    total: 4,
    returned: 4,
    rows: [
      baseRow({ id: "w", status: "waiting" }),
      baseRow({ id: "f", status: "in_flight" }),
      baseRow({ id: "r", status: "replied", replyPreview: "answer text" }),
      baseRow({ id: "x", status: "refused", statusDetail: "refused_provenance" }),
      baseRow({ id: "u", status: "some_future_state" }),
    ],
  };
  const { bindings } = makeBindings({ snapshot: snap });
  const { act, root } = await renderPanel(bindings);
  ok(q(".collab-dispatch__status--waiting").length === 1, "等待徽标渲染");
  ok(q(".collab-dispatch__status--in_flight").length === 1, "进行中徽标渲染");
  ok(q(".collab-dispatch__status--replied").length === 1, "已回信徽标渲染");
  ok(q(".collab-dispatch__status--refused").length === 1, "已拒绝徽标渲染");
  ok(q(".collab-dispatch__status").length === 4, "未知状态诚实缺席（共 4 枚徽标，非 5）");
  ok(q(".collab-dispatch__reply").length === 1 && text().includes("answer text"), "已回信行带回信摘要");
  ok(text().includes("refused_provenance"), "拒绝原因 statusDetail 可见");
  await act(async () => { root.unmount(); });
}

async function testRolesRender() {
  process.stdout.write("② 角色徽标按 toRole 渲染，未知缺席\n");
  const snap: Snapshot = {
    total: 2,
    returned: 2,
    rows: [
      baseRow({ id: "a", toRole: "main" }),
      baseRow({ id: "b", toRole: "mystery" }),
    ],
  };
  const { bindings } = makeBindings({ snapshot: snap });
  const { act, root } = await renderPanel(bindings);
  ok(q(".collab-dispatch__role--main").length === 1, "main 角色徽标渲染");
  ok(q(".collab-dispatch__role").length === 1, "未知角色诚实缺席（共 1 枚角色徽标）");
  await act(async () => { root.unmount(); });
}

const roster: DirectoryRow[] = [
  {
    contactId: "sc_sub",
    title: "执行线会话",
    topicId: "topic-1",
    sessionPath: "C:\\sessions\\sub",
    scope: "project",
    workspaceRoot: "C:\\ws",
    open: true,
  },
];

async function testClickJumpsToTargetSession() {
  process.stdout.write("③ 点击行直达目标会话\n");
  const snap: Snapshot = { total: 1, returned: 1, rows: [baseRow({ id: "j", status: "in_flight" })] };
  const { bindings, calls } = makeBindings({ snapshot: snap, roster });
  const { act, root } = await renderPanel(bindings);
  const row = q(".collab-dispatch__row")[0] as HTMLButtonElement | undefined;
  ok(Boolean(row), "行渲染为可点按钮");
  await act(async () => {
    row!.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flush(30);
  });
  ok(calls.activate.length === 1, "ActivateTopic 恰好调用一次");
  if (calls.activate.length === 1) {
    const a = calls.activate[0];
    ok(a.scope === "project", `scope 来自目录行 (=${a.scope})`);
    ok(a.workspaceRoot === "C:\\ws", "workspaceRoot 来自目录行");
    ok(a.topicId === "topic-1", "topicId 来自目录行");
    ok(a.sessionPath === "C:\\sessions\\sub", "sessionPath 来自目录行");
  }
  await act(async () => { root.unmount(); });
}

async function testClickMissShowsNote() {
  process.stdout.write("④ 目录未命中 → 就地说明不静默\n");
  const snap: Snapshot = { total: 1, returned: 1, rows: [baseRow({ id: "m", status: "waiting" })] };
  const { bindings, calls } = makeBindings({ snapshot: snap, roster: [] });
  const { act, root } = await renderPanel(bindings);
  const row = q(".collab-dispatch__row")[0] as HTMLButtonElement;
  await act(async () => {
    row.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flush(30);
  });
  ok(calls.activate.length === 0, "未命中不触发跳转");
  ok(q(".collab-dispatch__note").length === 1 && text().includes("sc_sub"), "未命中说明可见（含 contact id）");
  await act(async () => { root.unmount(); });
}

async function testEmptyDegradedError() {
  process.stdout.write("⑤ 空态/降级/失败三态分离\n");
  // 空态
  {
    const { bindings } = makeBindings({ snapshot: { rows: [], total: 0, returned: 0 } });
    const { act, root } = await renderPanel(bindings);
    ok(q(".collab-dispatch__empty").length === 1, "健康空 = 得体空态");
    ok(!text().includes("collabDispatch."), "空态不漏 key 原文");
    await act(async () => { root.unmount(); });
  }
  // degraded
  {
    const { bindings } = makeBindings({ snapshot: { rows: [], total: 0, returned: 0, degraded: true } });
    const { act, root } = await renderPanel(bindings);
    ok(q(".collab-dispatch__empty--degraded").length === 1, "degraded ≠ 真空态");
    await act(async () => { root.unmount(); });
  }
  // 读取失败
  {
    const { bindings } = makeBindings({ reject: true });
    const { act, root } = await renderPanel(bindings);
    ok(q(".collab-dispatch__empty--error").length === 1, "失败显式报错块");
    ok(q(".collab-dispatch__empty--error button").length === 1, "失败块带重试按钮");
    await act(async () => { root.unmount(); });
  }
}

async function main() {
  installDom();
  await testFourStatesRender();
  await testRolesRender();
  await testClickJumpsToTargetSession();
  await testClickMissShowsNote();
  await testEmptyDegradedError();
  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
  process.exit(0);
}

void main();
