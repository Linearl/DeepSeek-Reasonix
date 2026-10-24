import { useEffect, useRef, useState } from "react";
import logoSymbol from "../assets/logo-symbol.svg";
import { useT } from "../lib/i18n";
import { markSplashShown } from "../lib/startupSplashState";

const MIN_VISIBLE_MS = 1400;
const FADE_OUT_MS = 420;
const MAX_HOLD_MS = 6000;

// tl 1132: the timing constants are injectable for the harness only — every
// default keeps the exact upstream value, so production behaviour is
// byte-for-byte unchanged.
export type StartupSplashTiming = {
  minVisibleMs?: number;
  fadeOutMs?: number;
  maxHoldMs?: number;
};

export function StartupSplash({ hold, onDone, timing }: { hold: boolean; onDone: () => void; timing?: StartupSplashTiming }) {
  const t = useT();
  const minVisibleMs = timing?.minVisibleMs ?? MIN_VISIBLE_MS;
  const fadeOutMs = timing?.fadeOutMs ?? FADE_OUT_MS;
  const maxHoldMs = timing?.maxHoldMs ?? MAX_HOLD_MS;
  const [minElapsed, setMinElapsed] = useState(false);
  const [forceRelease, setForceRelease] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const finishedRef = useRef(false);
  const onDoneRef = useRef(onDone);
  onDoneRef.current = onDone;

  const finish = (skipHold = false) => {
    if (finishedRef.current) return;
    if (!skipHold && (!minElapsed || hold) && !forceRelease) return;
    finishedRef.current = true;
    setLeaving(true);
    window.setTimeout(() => {
      markSplashShown();
      onDoneRef.current();
    }, fadeOutMs);
  };

  useEffect(() => {
    const minTimer = window.setTimeout(() => setMinElapsed(true), minVisibleMs);
    const maxTimer = window.setTimeout(() => setForceRelease(true), maxHoldMs);
    return () => {
      window.clearTimeout(minTimer);
      window.clearTimeout(maxTimer);
    };
  }, []);

  useEffect(() => {
    finish();
  }, [minElapsed, hold, forceRelease]);

  useEffect(() => {
    const skip = (event: KeyboardEvent) => {
      if (event.key !== "Escape" && event.key !== "Enter" && event.key !== " ") return;
      finish(true);
    };
    window.addEventListener("keydown", skip);
    return () => window.removeEventListener("keydown", skip);
  }, []);

  return (
    <div className="startup-splash" data-leaving={leaving} onClick={() => finish(true)}>
      <div className="startup-splash__card">
        <div className="startup-splash__mark" aria-hidden="true">
          <img src={logoSymbol} alt="" draggable={false} />
        </div>
        <div className="startup-splash__name">Reasonix</div>
        <div className="startup-splash__sub">{t("app.splashSubtitle")}</div>
        <div className="startup-splash__dots" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
      </div>
    </div>
  );
}
