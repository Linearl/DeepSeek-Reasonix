// 任务 563 — live on/off state for the lab picks wall. ExperimentalSection
// derives the map from its `features` array (the same render table the rail
// reads, recomputed on every settings update) and provides it here; the wall
// consumes it inside the intro dialog. Context instead of props keeps the
// task-379/562 pinned mount JSX (`<LabPicksWall t={t} />`, dialog mount line)
// byte-identical — guard suites pin those strings.

import { createContext, useContext } from "react";

/** pick/feature id → live on state. Keys are the 16 LAB_WALL_PICKS ids. */
export type LabWallOnMap = Readonly<Record<string, boolean>>;

/** Default {} ⇒ an unwired wall reads every pick as off (the honest default:
 * a broken provider shows "off + 建议开启" everywhere instead of lying). */
export const LabWallOnContext = createContext<LabWallOnMap>({});

export function useLabWallOn(): LabWallOnMap {
  return useContext(LabWallOnContext);
}

/** Read one pick's live state. Unknown id (map not yet populated) ⇒ off. */
export function labWallOnFor(map: LabWallOnMap, id: string): boolean {
  return map[id] ?? false;
}
