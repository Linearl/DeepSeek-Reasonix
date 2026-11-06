// tl1468 real-Chromium fixture: the artifacts dock tab mounted inside a
// fixed-size .workbench-dock__body replica (flex column + the production dock
// body rules), so scrollHeight/clientHeight can be measured in the three
// states jsdom cannot lay out: few rows / exactly full / overflowing.
import { useLayoutEffect, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { SideFilesDockPanel } from "../src/components/SessionSideFilesPanel";
import { LocaleProvider } from "../src/lib/i18n";
import "../src/styles.css";

type Options = {
  bodyHeight: number;
  bodyWidth: number;
  artifacts: number;
  variant: "artifacts" | "references";
};

declare global {
  interface Window {
    sideFilesDockFixture: {
      configure: (next: Partial<Options>) => void;
      revision: number;
      state: () => {
        dock: { scrollHeight: number; clientHeight: number; scrollTop: number };
        list: { scrollHeight: number; clientHeight: number };
        rows: number;
        foot: boolean;
      };
    };
  }
}

// Minimal host stub (tl1437 harness shape): the dock list only touches the
// bridge from the row menu, which this fixture never opens — but a defined
// window.go keeps lib/bridge's host detection deterministic in plain Chromium.
(window as unknown as { go: unknown }).go = {
  main: { App: {
    ResolveOpenablePathForTab: async () => "",
    ResolveWorkspacePathForTab: async (_tab: string, path: string) => path,
    OpenLocalPath: async () => {},
    RevealPath: async () => {},
  } },
};

const WORKSPACE_ROOT = "C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\global-workspace";
const SESSION_PATH = "C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\sessions\\s-bench.jsonl";

function Fixture() {
  const [options, setOptions] = useState<Options>({ bodyHeight: 420, bodyWidth: 320, artifacts: 3, variant: "artifacts" });
  const [revision, setRevision] = useState(0);
  const items = useMemo(() => Array.from({ length: options.artifacts }, (_, index) => ({
    kind: "tool",
    name: "write_file",
    args: JSON.stringify({ path: `${WORKSPACE_ROOT}\\tl1468\\gen\\file-${String(index).padStart(2, "0")}.txt`, content: "x" }),
  })), [options.artifacts]);
  useLayoutEffect(() => {
    document.documentElement.dataset.themeStyle = "graphite";
    document.documentElement.dataset.theme = "light";
    document.documentElement.dataset.platform = "windows";
    window.sideFilesDockFixture = {
      configure: (next) => { setOptions((current) => ({ ...current, ...next })); setRevision((value) => value + 1); },
      revision,
      state: () => {
        const dock = document.querySelector<HTMLElement>(".side-files--dock")!;
        const list = document.querySelector<HTMLElement>(".side-files__list")!;
        return {
          dock: { scrollHeight: dock.scrollHeight, clientHeight: dock.clientHeight, scrollTop: dock.scrollTop },
          list: { scrollHeight: list.scrollHeight, clientHeight: list.clientHeight },
          rows: document.querySelectorAll(".side-files__row").length,
          foot: document.querySelector(".side-files__foot") !== null,
        };
      },
    };
  }, [revision]);
  return (
    <div style={{ display: "flex", flexDirection: "column", height: options.bodyHeight, width: options.bodyWidth }}>
      <div className="workbench-dock__body">
        <LocaleProvider locale="en">
          <SideFilesDockPanel
            items={items}
            variant={options.variant}
            tabId="bench-tab"
            workspaceRoot={WORKSPACE_ROOT}
            sessionPath={SESSION_PATH}
          />
        </LocaleProvider>
      </div>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(<Fixture />);
