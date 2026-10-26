import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { labFlagEnabled, onLabFlagsChange } from "./labFlags";
import { SmoothTailFollower } from "./smoothTailFollow";

/** Keeps a running reasoning pane at its own tail until the reader scrolls up.
 *
 * tl 1165: with the smooth-scroll-follow lab switch off (default) the follow
 * stays the exact pre-1165 instant `scrollTop = scrollHeight` assignment with
 * no extra listeners (byte-for-byte); on, tail writes ride the DSH ScrollFollow
 * guards — native smooth motion, an outstanding target finishes before another
 * is issued, reader gestures cancel the animation, reduced motion jumps. */
export function useReasoningScrollFollow(
  content: string,
  active: boolean,
) {
  const elementRef = useRef<HTMLDivElement>(null);
  const followRef = useRef(true);
  const followerRef = useRef<SmoothTailFollower | null>(null);
  if (!followerRef.current) followerRef.current = new SmoothTailFollower();
  const [smoothEnabled, setSmoothEnabled] = useState(() => labFlagEnabled("smoothScrollFollow"));

  useEffect(() => onLabFlagsChange(() => setSmoothEnabled(labFlagEnabled("smoothScrollFollow"))), []);

  const onScroll = () => {
    const element = elementRef.current!;
    // Native follow motion is not reader movement: only settle the animation
    // on arrival and leave the follow intent alone until the gesture entries
    // below have cancelled it (DSH sample semantics).
    if (smoothEnabled && followerRef.current!.animating) {
      followerRef.current!.noteScroll(element);
      return;
    }
    followRef.current = element.scrollHeight - element.scrollTop - element.clientHeight <= 8;
  };

  useLayoutEffect(() => {
    if (!active || !followRef.current) return;
    const element = elementRef.current;
    if (!element) return;
    // This is the reasoning pane's own nested scroller, not the transcript.
    if (!smoothEnabled) {
      element.scrollTop = element.scrollHeight;
      return;
    }
    followerRef.current!.follow(element);
  }, [active, content, smoothEnabled]);

  // Reader-gesture cancel (DSH interrupt): only armed while the smooth follow
  // is on — the off state arms no listeners at all (铁律 2). Wheel/touch/
  // pointer all predate the scroll events the animation itself produces, so a
  // genuine upward gesture always wins the race.
  useEffect(() => {
    if (!smoothEnabled || !active) return;
    const element = elementRef.current;
    if (!element) return;
    const cancel = () => followerRef.current!.interrupt(element);
    const options = { passive: true, capture: true } as const;
    element.addEventListener("wheel", cancel, options);
    element.addEventListener("touchstart", cancel, options);
    element.addEventListener("pointerdown", cancel, options);
    return () => {
      element.removeEventListener("wheel", cancel, options);
      element.removeEventListener("touchstart", cancel, options);
      element.removeEventListener("pointerdown", cancel, options);
    };
  }, [smoothEnabled, active]);

  return [elementRef, onScroll] as const;
}
