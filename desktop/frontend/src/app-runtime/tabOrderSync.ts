// Keeps the manual tab order in step with the tab registry (task 38 batch C1).
//
// New tabs append, closed tabs drop, and the array identity is preserved when
// nothing changed so downstream memos stay quiet. The dependency is the tab list
// itself, not a fresh id array - deriving ids inside the effect is what keeps it
// from re-running on every render.
import { useEffect } from "react";
import type { TabMeta } from "../lib/types";

export function useTabOrderSync(
  tabMetas: readonly TabMeta[],
  setTabOrderIds: (updater: (current: string[]) => string[]) => void,
) {
  useEffect(() => {
    const ids = tabMetas.map((tab) => tab.id);
    setTabOrderIds((current) => {
      const next = current.filter((id) => ids.includes(id));
      for (const id of ids) {
        if (!next.includes(id)) next.push(id);
      }
      return next.join("\u0000") === current.join("\u0000") ? current : next;
    });
  }, [tabMetas, setTabOrderIds]);
}
