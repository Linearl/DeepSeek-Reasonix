// 任务461-P7 三级终止: the stop button's view-state machine (pure, testable).
// The backend owns the authoritative escalation level (RuntimeStatus mirror);
// this only decides what the button shows and what the next click does.

export type StopButtonPhase = "idle" | "normal" | "armed" | "grace" | "force";

export type StopButtonView = {
  phase: StopButtonPhase;
  /** Grace countdown seconds (ceil); null outside the grace. */
  countdownSeconds: number | null;
};

export function stopButtonView(input: {
  stopLevel: number;
  stopDeadlineUnix: number;
  /** When THIS surface fired the L1 stop (the first click); null before. */
  stopInitiatedAt: number | null;
  running: boolean;
  now: number;
  /** How long after the L1 click the button arms into 强制停止 (default 1s). */
  armAfterMs?: number;
}): StopButtonView {
  const armAfterMs = input.armAfterMs ?? 1000;
  const localLevel = input.stopInitiatedAt != null ? 1 : 0;
  const level = Math.max(input.stopLevel, localLevel);
  if (!input.running || level <= 0) return { phase: "idle", countdownSeconds: null };
  if (level >= 3) return { phase: "force", countdownSeconds: null };
  if (level >= 2) {
    const remaining = input.stopDeadlineUnix > 0
      ? Math.max(0, Math.ceil(input.stopDeadlineUnix - input.now / 1000))
      : null;
    return { phase: "grace", countdownSeconds: remaining };
  }
  const armed = input.stopInitiatedAt != null && input.now - input.stopInitiatedAt >= armAfterMs;
  return { phase: armed ? "armed" : "normal", countdownSeconds: null };
}
