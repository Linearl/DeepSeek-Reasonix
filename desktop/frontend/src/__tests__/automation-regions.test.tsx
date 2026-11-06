import assert from "node:assert/strict";

import { renderToStaticMarkup } from "react-dom/server";
import { JSDOM } from "jsdom";
import { AppBottomRegions } from "../app-shell/AppBottomRegions";
import { register } from "node:module";
import type { Translator } from "../lib/i18n";

const noop = () => {};
register(new URL("../../scripts/svg-loader.mjs", import.meta.url));
const { SidebarRegion } = await import("../app-shell/SidebarRegion");
const t = ((key: string) => key) as Translator;
// tl1450: the creation layout retired — the projection matrix covers the two
// survivors.
for (const layout of ["classic", "workbench"]) {
  for (const automation of [false, true]) {
    const markup = renderToStaticMarkup(<>
      <SidebarRegion className="sidebar" workbench={layout === "workbench"}
        collapsed={false} automation={automation}
        navTooltipDisabled t={t}
        onNewSession={noop} onOpenTrash={noop} onOpenAutomation={noop} onOpenSettings={noop}
        resize={{ min: 180, max: 400, value: 240, onPointerDown: noop, onKeyDown: noop, onReset: noop }}
        projectTree={{ onOpenTopic: noop, onCreateTopic: noop, onTopicsChanged: noop }} />
      <AppBottomRegions terminal={{ surfaceVisible: !automation, open: !automation,
        contentVisible: false, remoteSurface: false, t,
        panel: { tabId: "tab", open: !automation, onClose: noop },
        resizer: { min: 100, max: 400, value: 240, onPointerDown: noop, onKeyDown: noop, onReset: noop },
      }} />
    </>);
    const dom = new JSDOM(markup);
    const doc = dom.window.document;
    assert.equal(doc.querySelectorAll(".terminal-drawer").length, 1, "terminal host survives page projection");
    assert.equal(doc.querySelector(".terminal-drawer")?.hasAttribute("inert"), automation);
    assert.equal(doc.querySelectorAll(".terminal-drawer-resizer").length, automation ? 0 : 1);
    assert.equal(doc.querySelectorAll(".sidebar-collapse-toggle").length, 0, "tl1450: the creation-only collapse toggle is gone from every layout");
    const automationButtons = [...doc.querySelectorAll("button")].filter(button => button.querySelector(".lucide-alarm-clock"));
    assert.equal(automationButtons.length, 1, `${layout} keeps exactly one Automation entry`);
    if (layout !== "workbench") assert.equal(automationButtons[0].getAttribute("aria-current"), automation ? "page" : null);
    dom.window.close();
  }
}
console.log("automation regions: shared three-layout page projection passed");
