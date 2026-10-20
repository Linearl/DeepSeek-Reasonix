export type PendingNavigationRequest<T extends object> = T & {
  seq: number;
  resolve: () => void;
};

export type NavigationCoalescingRefs<T extends object> = {
  seqRef: { current: number };
  runningRef: { current: boolean };
  pendingRef: { current: PendingNavigationRequest<T> | null };
};

export function enqueueNavigationRequest<T extends object>(
  refs: NavigationCoalescingRefs<T>,
  input: T,
  run: (request: PendingNavigationRequest<T>) => Promise<void>,
): Promise<void> {
  const seq = refs.seqRef.current + 1;
  refs.seqRef.current = seq;

  let resolve!: () => void;
  const promise = new Promise<void>((res) => {
    resolve = res;
  });
  const request: PendingNavigationRequest<T> = { ...input, seq, resolve };

  const start = (next: PendingNavigationRequest<T>) => {
    refs.runningRef.current = true;
    void (async () => {
      try {
        await run(next);
      } catch {
        // run() is expected to handle user-visible failures. The scheduler keeps
        // navigation promises non-rejecting so event handlers cannot leak
        // unhandled rejections.
      } finally {
        next.resolve();
        const pending = refs.pendingRef.current;
        if (pending) {
          refs.pendingRef.current = null;
          start(pending);
        } else {
          refs.runningRef.current = false;
        }
      }
    })();
  };

  if (refs.runningRef.current) {
    refs.pendingRef.current?.resolve();
    refs.pendingRef.current = request;
    return promise;
  }

  start(request);
  return promise;
}

export type PendingOpenTopicRequest = PendingNavigationRequest<{
  scope: string;
  workspaceRoot: string;
  topicId: string;
  sessionPath?: string;
}>;

export type OpenTopicRequestInput = Omit<PendingOpenTopicRequest, "seq" | "resolve">;

export type OpenTopicCoalescingRefs = NavigationCoalescingRefs<OpenTopicRequestInput>;

/**
 * 任务 1026（fork issue #61）：导航遮罩超时兜底的队列解锁。后端导航调用挂死时
 * run() 永不返回 ⇒ finally 永不执行 ⇒ runningRef 永久为真，后续导航全部进
 * pending 且永不执行。解锁 = seq 前移（挂死请求日后返回时 latest() 守卫判其
 * stale，零 UI 污染）+ running 复位 + 顶掉 pending（按 enqueue 既有语义先
 * resolve，await 方不再悬挂）。
 */
export function forceUnlockNavigationQueue<T extends object>(refs: NavigationCoalescingRefs<T>): void {
  refs.seqRef.current += 1;
  refs.runningRef.current = false;
  const stranded = refs.pendingRef.current;
  refs.pendingRef.current = null;
  stranded?.resolve();
}

export function enqueueOpenTopicRequest(
  refs: OpenTopicCoalescingRefs,
  input: OpenTopicRequestInput,
  run: (request: PendingOpenTopicRequest) => Promise<void>,
): Promise<void> {
  return enqueueNavigationRequest(refs, input, run);
}
