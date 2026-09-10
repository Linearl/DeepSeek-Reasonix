// Viewport and live-resize metrics, moved out of App.tsx (task 38 batch B2).
//
// These seven values describe geometry only: the window size, and the transient
// widths a drag reports before the drop commits. Keeping them together means the
// resize interactions read one owner instead of reaching into the monolith.
import { useState } from "react";

export function useViewportMetricsOwner() {
  const [sidebarResizing, setSidebarResizing] = useState(false);
  const [liveSidebarWidth, setLiveSidebarWidth] = useState<number | null>(null);
  const [viewportWidth, setViewportWidth] = useState(() =>
    typeof window === "undefined" ? 1440 : window.innerWidth,
  );
  const [viewportHeight, setViewportHeight] = useState(() =>
    typeof window === "undefined" ? 720 : window.innerHeight,
  );
  const [workspacePanelResizing, setWorkspacePanelResizing] = useState(false);
  const [liveWorkspacePanelRenderWidth, setLiveWorkspacePanelRenderWidth] = useState<number | null>(null);
  const [liveTerminalHeight, setLiveTerminalHeight] = useState<number | null>(null);

  return {
    sidebarResizing,
    setSidebarResizing,
    liveSidebarWidth,
    setLiveSidebarWidth,
    viewportWidth,
    setViewportWidth,
    viewportHeight,
    setViewportHeight,
    workspacePanelResizing,
    setWorkspacePanelResizing,
    liveWorkspacePanelRenderWidth,
    setLiveWorkspacePanelRenderWidth,
    liveTerminalHeight,
    setLiveTerminalHeight,
  };
}
