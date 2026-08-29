import type { RefObject } from "react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { TranscriptScrollMode } from "./transcriptScrollArbiter";
import { noteTranscriptScrollWrite } from "./transcriptScrollProbe";

export type TranscriptScrollWriterRequest = {
  owner: string;
  operation: "scrollTo" | "scrollBy" | "scrollToIndex";
  source: string;
  top?: number;
  index?: number | "LAST";
  behavior?: ScrollBehavior;
  align?: "start" | "center" | "end";
  phase?: "initial" | "settle";
  expectedGeneration: number;
  geometryRevision: number;
  settleFrame?: number;
  offBottomFrames?: number;
  stagnantFrames?: number;
};

export type TranscriptScrollWriter = {
  write: (request: TranscriptScrollWriterRequest) => boolean;
  lastOwner: () => string | undefined;
};

/** The only production gateway allowed to issue imperative Transcript writes. */
export function createTranscriptScrollWriter({
  virtuosoRef,
  scrollRef,
  modeRef,
  generationRef,
}: {
  virtuosoRef: RefObject<VirtuosoHandle | null>;
  scrollRef: RefObject<HTMLDivElement | null>;
  modeRef: RefObject<TranscriptScrollMode>;
  generationRef: RefObject<number>;
}): TranscriptScrollWriter {
  let sequence = 0;
  let previousOwner: string | undefined;

  const write = (request: TranscriptScrollWriterRequest): boolean => {
    const handle = virtuosoRef.current;
    const element = scrollRef.current;
    const generation = generationRef.current;
    if (!handle || !element || modeRef.current === "native-thumb") return false;
    if (request.expectedGeneration !== generation) return false;
    if (request.operation === "scrollToIndex" ? request.index === undefined : request.top === undefined) return false;

    sequence += 1;
    previousOwner = request.owner;
    noteTranscriptScrollWrite({
      owner: request.owner,
      kind: request.operation,
      top: request.top,
      index: request.index,
      source: request.source,
      phase: request.phase,
      scrollTop: element.scrollTop,
      scrollHeight: element.scrollHeight,
      clientHeight: element.clientHeight,
      bottomDistance: element.scrollHeight - element.scrollTop - element.clientHeight,
      mode: modeRef.current,
      sequence,
      generation,
      geometryRevision: request.geometryRevision,
      settleFrame: request.settleFrame,
      offBottomFrames: request.offBottomFrames,
      stagnantFrames: request.stagnantFrames,
    });

    const behavior = request.behavior === "smooth" ? "smooth" : "auto";
    if (request.operation === "scrollTo") handle.scrollTo({ top: request.top!, behavior });
    else if (request.operation === "scrollBy") handle.scrollBy({ top: request.top!, behavior });
    else handle.scrollToIndex({ index: request.index!, align: request.align ?? "start", behavior });
    return true;
  };

  return { write, lastOwner: () => previousOwner };
}
