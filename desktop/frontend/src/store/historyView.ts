// historyView holds the history/trash overlay — which list is open and which sessions
// it shows. It lives in a store rather than App-local useState so that the entry point
// and the compositions that refresh it (useAppNavigationComposition,
// useAppSessionComposition) work from one value instead of a prop chain (task 38).
//
// The setter keeps the Dispatch<SetStateAction<...>> shape both compositions already
// declare, so passing it through stays type-compatible.

import { create } from "zustand";
import type { Dispatch, SetStateAction } from "react";
import type { HistoryViewState } from "../app-runtime/historyViewProjection";

type HistoryViewStore = {
  histView: HistoryViewState | null;
  setHistView: Dispatch<SetStateAction<HistoryViewState | null>>;
};

export const useHistoryViewStore = create<HistoryViewStore>((set) => ({
  histView: null,
  setHistView: (update) =>
    set((current) => ({
      histView:
        typeof update === "function"
          ? (update as (previous: HistoryViewState | null) => HistoryViewState | null)(current.histView)
          : update,
    })),
}));
