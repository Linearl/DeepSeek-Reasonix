// tl1345 real-Chromium fixture: the quick-command manager dialog mounted with
// the production CSS (styles.css + ProviderAccessSettings.css) and a
// configurable entry count, so the vertical chain (shell cap -> panel cap ->
// inner scroll) can be measured in the states jsdom cannot lay out: few rows
// (no scroll) and many rows (panel scrolls, dialog stays inside the viewport).
import { useLayoutEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { QuickCommandsManagerDialog } from "../src/components/QuickCommandsManagerDialog";
import { LocaleProvider } from "../src/lib/i18n";
import "../src/styles.css";
// The wide shell chain lives here in production (imported by
// SettingsPanelEntry.tsx); the fixture mounts the dialog standalone, so the
// import must be explicit or .provider-dialog loses its height/width rules.
import "../src/components/ProviderAccessSettings.css";

type Options = { entries: number };

declare global {
  interface Window {
    quickCommandsDialogFixture: {
      configure: (next: Partial<Options>) => void;
      revision: number;
      state: () => {
        dialog: { height: number; bottom: number };
        panel: { scrollHeight: number; clientHeight: number; maxHeight: string; overflowY: string; scrollTop: number };
        rows: number;
        viewportHeight: number;
      };
    };
  }
}

function Fixture() {
  const [options, setOptions] = useState<Options>({ entries: 3 });
  const [revision, setRevision] = useState(0);
  const [open, setOpen] = useState(true);
  const entries = Array.from({ length: options.entries }, (_, index) => ({
    title: `Command ${String(index + 1).padStart(2, "0")}`,
    text: `snippet body for command ${index + 1} — a realistic one-line description used by the composer`,
    enabled: true,
  }));
  useLayoutEffect(() => {
    document.documentElement.dataset.themeStyle = "graphite";
    document.documentElement.dataset.theme = "light";
    document.documentElement.dataset.platform = "windows";
    window.quickCommandsDialogFixture = {
      configure: (next) => { setOptions((current) => ({ ...current, ...next })); setOpen(true); setRevision((value) => value + 1); },
      revision,
      state: () => {
        const dialog = document.querySelector<HTMLElement>(".modal.provider-dialog")!;
        const panel = document.querySelector<HTMLElement>(".settings-quick-commands--panel")!;
        const panelStyle = getComputedStyle(panel);
        const rect = dialog.getBoundingClientRect();
        return {
          dialog: { height: rect.height, bottom: rect.bottom },
          panel: {
            scrollHeight: panel.scrollHeight,
            clientHeight: panel.clientHeight,
            maxHeight: panelStyle.maxHeight,
            overflowY: panelStyle.overflowY,
            scrollTop: panel.scrollTop,
          },
          rows: document.querySelectorAll(".settings-quick-commands__row").length,
          viewportHeight: window.innerHeight,
        };
      },
    };
  }, [revision]);
  return (
    <LocaleProvider locale="en">
      {open && (
        <QuickCommandsManagerDialog
          entries={entries}
          busy={false}
          save={async () => {}}
          onClose={() => setOpen(false)}
        />
      )}
    </LocaleProvider>
  );
}

createRoot(document.getElementById("root")!).render(<Fixture />);
