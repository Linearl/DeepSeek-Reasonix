// Transcript reveal signals plus the project-tree time filter (task 38 batch B3).
//
// The reveal signals are counters: bumping one tells the transcript to scroll to
// a newly revealed item. The time filter persists itself, so the loader and the
// persisting effect live here together instead of being split across App.tsx.
import { useEffect, useState } from "react";

export type ProjectTreeTimeFilter = "all" | "10" | "20" | "1h" | "3h" | "5h" | "1d";

const TIME_FILTER_STORAGE_KEY = "projectTree:timeFilter";

function readStoredTimeFilter(): ProjectTreeTimeFilter {
  try {
    const saved = localStorage.getItem(TIME_FILTER_STORAGE_KEY);
    if (saved === "all" || saved === "10" || saved === "20" || saved === "1h" || saved === "3h" || saved === "5h" || saved === "1d") {
      return saved;
    }
  } catch {
    /* localStorage unavailable */
  }
  return "all";
}

export function useTranscriptRevealOwner() {
  const [tabRevealSignal, setTabRevealSignal] = useState(0);
  const [transcriptRevealSignal, setTranscriptRevealSignal] = useState(0);
  const [topicTimeFilter, setTopicTimeFilter] = useState<ProjectTreeTimeFilter>(readStoredTimeFilter);

  useEffect(() => {
    try {
      localStorage.setItem(TIME_FILTER_STORAGE_KEY, topicTimeFilter);
    } catch {
      /* ignore */
    }
  }, [topicTimeFilter]);

  return {
    tabRevealSignal,
    setTabRevealSignal,
    transcriptRevealSignal,
    setTranscriptRevealSignal,
    topicTimeFilter,
    setTopicTimeFilter,
  };
}
