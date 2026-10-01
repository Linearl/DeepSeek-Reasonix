// Task 399: find-highlight context. Lives outside the transcriptFind lib so
// that module stays React-free; rows consume it through this tiny context so
// the highlight state does not have to be threaded through Viewport →
// ProjectionView → BlockView as props (which would bust memo on every query
// keystroke for every block).

import { createContext, useContext } from "react";

import type { TranscriptFindHighlight } from "../lib/transcriptFind";

export const TranscriptFindContext = createContext<TranscriptFindHighlight>(null);

export const useTranscriptFindHighlight = (): TranscriptFindHighlight => useContext(TranscriptFindContext);
