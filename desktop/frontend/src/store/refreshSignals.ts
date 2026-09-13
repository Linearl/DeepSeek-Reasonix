// refreshSignals owns the counter-style invalidation signals the workspace regions
// use to refetch: the right dock tree, the composer's file-reference chips and the
// project catalogue. Three separate counters rather than one shared one, because
// bumping the dock must not drag the composer into a refetch — and they live in a
// store so a region can be refreshed without threading callbacks through the tree
// (task 38).

import { create } from "zustand";

type RefreshSignalsState = {
  dockRefreshKey: number;
  fileRefRefreshKey: number;
  projectRevision: number;
  workspaceControllerEpoch: number;
};

export const useRefreshSignalsStore = create<RefreshSignalsState>(() => ({
  dockRefreshKey: 0,
  fileRefRefreshKey: 0,
  projectRevision: 0,
  workspaceControllerEpoch: 0,
}));

export const bumpDockRefresh = (): void => {
  useRefreshSignalsStore.setState((current) => ({ dockRefreshKey: current.dockRefreshKey + 1 }));
};

export const bumpFileRefRefresh = (): void => {
  useRefreshSignalsStore.setState((current) => ({ fileRefRefreshKey: current.fileRefRefreshKey + 1 }));
};

export const bumpProjectRevision = (): void => {
  useRefreshSignalsStore.setState((current) => ({ projectRevision: current.projectRevision + 1 }));
};

export const bumpWorkspaceControllerEpoch = (): void => {
  useRefreshSignalsStore.setState((current) => ({ workspaceControllerEpoch: current.workspaceControllerEpoch + 1 }));
};
