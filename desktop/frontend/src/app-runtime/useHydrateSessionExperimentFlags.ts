import { useEffect } from "react";
import { app } from "../lib/bridge";
import { setSessionMonitorEnabled } from "../lib/sessionMonitor";
import { setFeedbackEnabled } from "../components/FeedbackPanel";

/**
 * Loads experimental session-monitor / feedback flags from the host once on
 * mount so the sidebar buttons appear after restart without a Settings click.
 * Lives in app-runtime so app-shell presentation stays off the bridge import
 * graph (check-app-layers).
 */
export function useHydrateSessionExperimentFlags(): void {
  useEffect(() => {
    let cancelled = false;
    void app
      .DesktopStartupSettings()
      .then((settings) => {
        if (cancelled || !settings) return;
        setSessionMonitorEnabled(Boolean(settings.experimentalSessionMonitor));
        setFeedbackEnabled(Boolean(settings.experimentalFeedback));
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);
}
