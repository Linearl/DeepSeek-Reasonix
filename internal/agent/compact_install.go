// The automatic fold's install tail: checkpoint body, CAS commit, success
// outcome, done telemetry.
package agent

import (
	"log/slog"

	"reasonix/internal/provider"
)

// foldProjectionInstall carries one prepared summary through installation.
type foldProjectionInstall struct {
	trigger                                 string
	msgs, canonical, fold, kept, bodySuffix []provider.Message
	retention                               userTurnRetention
	summary                                 string
	head, covered, sourceTokens             int
	transcriptVersion                       uint64
	startProjectionVersion, startGeneration uint64
	activeTurn                              int64
	viewInputHash                           string
	res                                     foldSummary
	tele                                    CompactionTelemetry
}

func (a *Agent) installFoldProjection(in foldProjectionInstall) (CompactionOutcome, error) {
	tele := in.tele
	// The projection body freezes only prefix + digest + kept messages; the
	// verbatim tail splices live from canonical[start:] so tail-side rewrites
	// (rewind truncation, snips) stay visible without rebuilding the fold.
	projMsgs := checkpointProjectionMessages(in.msgs, in.head, in.kept, in.summary)
	if len(in.bodySuffix) > 0 {
		projMsgs = append(projMsgs, projectionMessagesPreservingPinnedContext(in.bodySuffix)...)
	}
	tele.UserTurnsKept, tele.UserTurnsDropped = in.retention.Kept, in.retention.Dropped
	projMsgs, spliced, projTokens, err := a.preparePinnedCheckpointCandidate(in.trigger, projMsgs, in.canonical, in.covered, in.sourceTokens, &tele)
	if err != nil {
		a.emitCompactionAborted(in.trigger)
		return CompactionNoop, err
	}
	viewOutputHash := providerVisibleFingerprint(modelInputMessages(spliced))
	_, err = a.commitSummaryProjection(summaryProjectionCommit{
		canonical: in.canonical, fold: in.fold, projected: projMsgs, result: in.res,
		transcriptVersion: in.transcriptVersion, projectionVersion: in.startProjectionVersion,
		generation: in.startGeneration, activeTurn: in.activeTurn, trigger: in.trigger,
		summary: in.summary, inputHash: in.viewInputHash, outputHash: viewOutputHash,
		sourceTokens: in.sourceTokens, projectionTokens: projTokens, covered: in.covered,
	})
	if err != nil {
		// 任务 867: the automatic path never recorded a stale CAS rejection —
		// now the spent request lands in both telemetry and the failed outcome.
		tele.Error = err.Error()
		a.emitCompactionTelemetry(tele)
		a.emitCompactionFailed(in.trigger, err)
		return CompactionNoop, err
	}
	a.emitCompactionSucceeded(in.trigger, len(in.fold), in.summary, "")
	// Done-log cache readout (task 303); 任务 867 adds fold_tokens so the
	// view estimate, summarizer input, and provider count stay separable.
	slog.Info("agent: compaction done", "trigger", in.trigger, "stage", "done",
		"folded_messages", len(in.fold), "source_tokens", in.sourceTokens,
		"fold_tokens", tele.FoldTokens,
		"projection_tokens", projTokens,
		"input_tokens", tele.InputTokens, "output_tokens", tele.OutputTokens,
		"cache_hit_tokens", tele.CacheHitTokens, "cache_miss_tokens", tele.CacheMissTokens,
		"request_count", tele.RequestCount)
	// Task 317 (cache 保全 面 b): the automatic fold attributes its miss the
	// same way the explicit tool does (shared helper, both entry points).
	a.logCompactionMissAttribution(in.trigger, tele, in.sourceTokens)
	return CompactionInstalled, nil
}
