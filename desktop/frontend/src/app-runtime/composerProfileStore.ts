// Composer profiles keyed by tab (task 38 batch B6).
//
// Only the store moves here. The derived profile for the active tab and the two
// patchers stay in App.tsx: with 17 and 11 references they are part of the
// monolith's control flow, not a self-contained cluster. Exporting the setter
// keeps those patchers working unchanged.
import { useState } from "react";
import type { ComposerProfile } from "../lib/composerProfile";

export function useComposerProfileStore() {
  const [composerProfilesByTab, setComposerProfilesByTab] = useState<Record<string, ComposerProfile>>({});
  return { composerProfilesByTab, setComposerProfilesByTab };
}
