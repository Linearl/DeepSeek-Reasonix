import { useEffect, useState } from "react";
import type { FilePreview } from "../lib/types";
import { useT } from "../lib/i18n";
import { workspaceBasename } from "../lib/workspacePanelFormat";

// tl 1110: HTML previews render in a sandboxed iframe. The default sandbox
// value keeps allow-same-origin (styles/images render) and withholds
// allow-scripts (scripts never execute). Enabling the explicit per-file
// toggle switches the sandbox to allow-scripts ONLY — allow-same-origin is
// always dropped when scripts run, because combining the two would hand the
// page a same-origin handle on the app shell (the media route is
// same-origin). The backend also ships a CSP sandbox header as defense in
// depth, but this attribute is the primary gate.
function htmlSandbox(scriptsAllowed: boolean): string {
  return scriptsAllowed ? "allow-scripts" : "allow-same-origin";
}

// Per-file localStorage key for the "allow scripts" toggle. Scoped by path so
// the preference survives reloads without leaking across files.
const scriptToggleStorageKey = (path: string) => `reasonix.workspace.htmlScripts:${path}`;

function readScriptToggle(path: string): boolean {
  try {
    return window.localStorage.getItem(scriptToggleStorageKey(path)) === "1";
  } catch {
    return false;
  }
}

function writeScriptToggle(path: string, allowed: boolean): void {
  try {
    if (allowed) {
      window.localStorage.setItem(scriptToggleStorageKey(path), "1");
    } else {
      window.localStorage.removeItem(scriptToggleStorageKey(path));
    }
  } catch {
    // localStorage unavailable — toggle stays session-only.
  }
}

export function WorkspaceMediaPreview({ preview }: { preview: FilePreview }) {
  const t = useT();
  const [scriptsAllowed, setScriptsAllowed] = useState(() =>
    preview.kind === "html" ? readScriptToggle(preview.path) : false,
  );

  // Keep the per-file toggle in sync when the previewed file changes (the
  // component may be reused across selections instead of remounting).
  useEffect(() => {
    setScriptsAllowed(preview.kind === "html" ? readScriptToggle(preview.path) : false);
  }, [preview.kind, preview.path]);

  if (!preview.url) return null;
  if (preview.kind === "image") {
    return (
      <div className="workspace-media workspace-media--image">
        <img src={preview.url} alt={workspaceBasename(preview.path)} decoding="async" draggable={false} />
      </div>
    );
  }
  if (preview.kind === "pdf") {
    return <iframe className="workspace-media workspace-media--pdf" src={preview.url} title={workspaceBasename(preview.path)} />;
  }
  if (preview.kind === "html") {
    return (
      <div className="workspace-media workspace-media--html">
        <iframe
          className="workspace-media__frame"
          src={preview.url}
          title={workspaceBasename(preview.path)}
          sandbox={htmlSandbox(scriptsAllowed)}
        />
        <label className="workspace-media__html-toggle">
          <input
            type="checkbox"
            checked={scriptsAllowed}
            onChange={(event) => {
              const next = event.target.checked;
              setScriptsAllowed(next);
              writeScriptToggle(preview.path, next);
            }}
          />
          <span className="workspace-media__html-toggle-label">
            {scriptsAllowed ? t("workspace.htmlScriptsOn") : t("workspace.htmlScriptsOff")}
          </span>
        </label>
      </div>
    );
  }
  return null;
}
