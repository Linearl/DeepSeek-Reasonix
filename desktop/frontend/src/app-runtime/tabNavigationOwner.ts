// Tab ordering plus two desktop preference mirrors (task 38 batch B7, the
// closing batch for the first pass through App.tsx).
//
// tabMetas deliberately stays in App.tsx: 24 references make it the tab list
// itself, which every projection reads — moving it is a redesign, not a move.
import { useState } from "react";
import type { QuickCommandEntry } from "../lib/settingsViewTypes";

export function useTabNavigationOwner() {
  const [tabOrderIds, setTabOrderIds] = useState<string[]>([]);
  const [navigationSurfaceIntent, setNavigationSurfaceIntent] = useState<number | null>(null);
  const [startupUpdateChecksEnabled, setStartupUpdateChecksEnabled] = useState<boolean | null>(null);
  const [quickCommands, setQuickCommands] = useState<QuickCommandEntry[]>([]);

  return {
    tabOrderIds,
    setTabOrderIds,
    navigationSurfaceIntent,
    setNavigationSurfaceIntent,
    startupUpdateChecksEnabled,
    setStartupUpdateChecksEnabled,
    quickCommands,
    setQuickCommands,
  };
}
