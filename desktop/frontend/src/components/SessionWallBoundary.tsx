import { Component, type ReactNode } from "react";
import { reportCrash } from "../lib/crash";

// Task 627: the session wall is an experimental overlay — if it ever throws at
// render time (chunk execution, data shape, …) the failure must take down the
// wall only, never the whole app. The root boundary renders null for a crashed
// tree, which would blank the entire window over a wall-sized bug. This local
// boundary swallows at the wall's edge and leaves the crash in desktop.log.
export class SessionWallBoundary extends Component<{ children: ReactNode }, { crashed: boolean }> {
  state = { crashed: false };

  static getDerivedStateFromError() {
    return { crashed: true };
  }

  componentDidCatch(error: unknown, info: { componentStack?: string | null }) {
    reportCrash("react:session-wall", error, info.componentStack ?? undefined);
  }

  render() {
    return this.state.crashed ? null : this.props.children;
  }
}
