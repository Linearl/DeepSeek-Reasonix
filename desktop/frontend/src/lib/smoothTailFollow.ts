/** tl 1165 — DSH ScrollFollow 式的流式平滑尾部跟随（四条防线缺一不可）。
 *
 * 参考实现：deepseek-harness packages/client/ui-chat/src/client/chat/use-scroll-follow.ts
 * （ScrollFollow 类）。只加 smooth 不配齐后三条会比现状更糟：连续高频 delta
 * 的 smooth 请求互相打断（比瞬移还卡），动画与读者手势抢滚动条（拽回用户），
 * 减少动效偏好被无视。四条映射：
 *  1. 跟随 = `element.scrollTo({ top, behavior: "smooth" })` 原生平滑动画；
 *  2. 上一个动画未完成不发新目标（outstanding-target gate）——结算靠 scroll
 *     事件到达检测 + 每次请求前的惰性检测，不依赖 scrollend 兼容性；
 *  3. 用户手势一滚立即取消动画并钉住当前位置（interrupt，不拽用户）；
 *  4. `prefers-reduced-motion: reduce` 退化为瞬间定位。
 *
 * 开关关闭时消费方根本不调用本类（既有瞬移语句原样保留，逐字节一致）。
 */

/** Near-bottom arrivals position immediately instead of animating (DSH
 * toBottom's own tolerance). */
export const SMOOTH_FOLLOW_NEAR_BOTTOM_PX = 4;

/** A scroll this close to the outstanding target settles the animation. */
export const SMOOTH_FOLLOW_SETTLE_PX = 4;

export class SmoothTailFollower {
  private target: number | null = null;

  /** Whether a native smooth follow animation has an outstanding target. */
  get animating(): boolean {
    return this.target !== null;
  }

  private static reducedMotion(): boolean {
    return typeof matchMedia === "function"
      && matchMedia("(prefers-reduced-motion: reduce)").matches;
  }

  private static floorOf(element: HTMLElement): number {
    return Math.max(0, element.scrollHeight - element.clientHeight);
  }

  /** Scroll events during native motion: a delivery at (or past) the target
   * settles it so the next follow request may issue a fresh goal. Reader
   * gestures never come through here — gesture entries call interrupt first. */
  noteScroll(element: HTMLElement): void {
    const target = this.target;
    if (target === null) return;
    const top = element.scrollTop;
    if (top >= Math.min(target, SmoothTailFollower.floorOf(element)) - SMOOTH_FOLLOW_SETTLE_PX) {
      this.target = null;
    }
  }

  /** Cancel native motion before a reader gesture, pinning the current
   * position instantly (DSH interrupt semantics). No-op while idle, so
   * unconditional gesture-entry calls stay byte-for-byte inert with the
   * switch off. */
  interrupt(element: HTMLElement): void {
    if (this.target === null) return;
    this.target = null;
    element.scrollTo({ top: element.scrollTop, behavior: "instant" });
  }

  /** Drop all state (surface generation change / unmount). */
  reset(): void {
    this.target = null;
  }

  /** Follow the measured tail (DSH toBottom semantics): immediate when
   * already near the floor or under reduced motion, smooth otherwise, and
   * skipped entirely while an earlier smooth target is still outstanding. */
  follow(element: HTMLElement): void {
    const floor = SmoothTailFollower.floorOf(element);
    const top = element.scrollTop;
    if (top >= floor || (!this.animating && floor - top <= SMOOTH_FOLLOW_NEAR_BOTTOM_PX)) {
      this.jump(element, floor);
      return;
    }
    if (SmoothTailFollower.reducedMotion()) {
      this.jump(element, floor);
      return;
    }
    if (this.animating) return;
    this.target = floor;
    element.scrollTo({ top: floor, behavior: "smooth" });
  }

  /** Position immediately; an in-flight animation is cancelled with a native
   * instant scroll so the browser does not keep animating past this write. */
  private jump(element: HTMLElement, floor: number): void {
    const animated = this.animating;
    this.target = null;
    if (animated) element.scrollTo({ top: floor, behavior: "instant" });
    else element.scrollTop = floor;
  }
}
