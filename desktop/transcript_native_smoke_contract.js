(() => {
  "use strict";

  const post = (payload) => {
    const message = JSON.stringify(payload);
    if (window.chrome?.webview?.postMessage) {
      window.chrome.webview.postMessage(message);
      return;
    }
    if (window.webkit?.messageHandlers?.reasonixNativeSmoke) {
      window.webkit.messageHandlers.reasonixNativeSmoke.postMessage(message);
    }
  };

  const waitFor = (predicate, timeout = 30000) => new Promise((resolve, reject) => {
    const startedAt = performance.now();
    const sample = () => {
      const value = predicate();
      if (value) {
        resolve(value);
        return;
      }
      if (performance.now() - startedAt >= timeout) {
        reject(new Error("native transcript fixture timed out"));
        return;
      }
      requestAnimationFrame(sample);
    };
    sample();
  });

  const state = {
    transcript: null,
    frames: [],
    active: false,
    growthTimer: 0,
    growthTicks: 0,
    growthSurface: null,
    initialDistance: 0,
    phase: "waiting-topic",
    writes: [],
    wheelEvents: 0,
    wheelDelta: 0,
    wheelInsideTranscript: 0,
    wheelMaxDelta: 0,
  };
  window.__reasonixNativeTranscriptSmokeState = state;
  window.__REASONIX_TRANSCRIPT_SCROLL_WRITE__ = (write) => {
    state.writes.push(write);
    if (state.writes.length > 80) state.writes.shift();
  };
  window.addEventListener("wheel", (event) => {
    if (!state.active) return;
    state.wheelEvents += 1;
    state.wheelDelta += event.deltaY;
    if (event.target instanceof Node && state.transcript?.contains(event.target)) state.wheelInsideTranscript += 1;
    state.wheelMaxDelta = Math.max(state.wheelMaxDelta, Math.abs(event.deltaY));
  }, { capture: true, passive: true });

  const visibleRows = (element) => {
    const viewport = element.getBoundingClientRect();
    return [...element.querySelectorAll(".transcript__row")].filter((row) => {
      const rect = row.getBoundingClientRect();
      return rect.bottom > viewport.top && rect.top < viewport.bottom;
    });
  };

  const outerReaderPoint = (element) => {
    const viewport = element.getBoundingClientRect();
    for (const row of visibleRows(element)) {
      const rect = row.getBoundingClientRect();
      const visibleTop = Math.max(viewport.top, rect.top);
      const visibleBottom = Math.min(viewport.bottom, rect.bottom);
      if (visibleBottom - visibleTop < 2) continue;
      const y = visibleTop + (visibleBottom - visibleTop) / 2;
      // Match the browser gate: row padding is owned by Transcript, while
      // code/table descendants may own their own nested scrollports.
      for (const x of [rect.left + 16, rect.right - 16]) {
        if (document.elementFromPoint(x, y) === row) {
          return { x: Math.round(x), y: Math.round(y) };
        }
      }
    }
    return null;
  };

  const scheduleSample = () => {
    requestAnimationFrame(() => window.setTimeout(sample, 0));
  };

  const sample = () => {
    if (!state.active || !(state.transcript instanceof HTMLElement)) return;
    const element = state.transcript;
    const viewport = element.getBoundingClientRect();
    const rows = visibleRows(element);
    const mounted = [...element.querySelectorAll(".transcript__row[data-index]")]
      .map((row) => Number.parseInt(row.dataset.index ?? "", 10))
      .filter(Number.isFinite);
    state.frames.push({
      top: element.scrollTop,
      height: element.scrollHeight,
      occupied: rows.length > 0,
      mode: element.dataset.scrollMode ?? "missing",
      readerIntent: element.dataset.transcriptReaderIntent ?? "missing",
      readerLayoutLease: element.dataset.transcriptReaderLayoutLease ?? "missing",
      mountedFirst: mounted.length > 0 ? Math.min(...mounted) : null,
      mountedLast: mounted.length > 0 ? Math.max(...mounted) : null,
      mountedCount: mounted.length,
      visible: rows.map((row) => ({
        index: row.dataset.index ?? "",
        top: row.getBoundingClientRect().top - viewport.top,
      })),
    });
    // ResizeObserver is delivered after rAF layout work but before paint.
    // Sample from the following task so the contract measures the geometry a
    // native WebView actually painted, not an intermediate pre-observer state.
    scheduleSample();
  };

  const growFooter = () => {
    if (!(state.growthSurface instanceof HTMLElement) || state.growthTicks >= 64) return;
    state.growthSurface.style.height = `${Number.parseFloat(state.growthSurface.style.height || "0") + 2}px`;
    state.growthTicks += 1;
  };

  const waitForStableViewport = (element, requiredFrames = 8, timeout = 10000) => new Promise((resolve, reject) => {
    const startedAt = performance.now();
    let previous = null;
    let stableFrames = 0;
    const recent = [];
    const sample = () => {
      const current = { top: element.scrollTop, height: element.scrollHeight, clientHeight: element.clientHeight };
      recent.push(current);
      if (recent.length > 12) recent.shift();
      const stable = previous
        && Math.abs(current.top - previous.top) <= 1
        && Math.abs(current.height - previous.height) <= 1
        && current.clientHeight === previous.clientHeight
        && visibleRows(element).length > 0;
      stableFrames = stable ? stableFrames + 1 : 0;
      previous = current;
      if (stableFrames >= requiredFrames) {
        resolve();
        return;
      }
      if (performance.now() - startedAt >= timeout) {
        reject(new Error(`native transcript viewport did not stabilize: ${JSON.stringify({
          stableFrames,
          recent,
          visibleRows: visibleRows(element).length,
          mode: element.dataset.scrollMode ?? "missing",
          rows: element.dataset.transcriptRowCount ?? "0",
        })}`));
        return;
      }
      requestAnimationFrame(sample);
    };
    requestAnimationFrame(sample);
  });

  const waitForStableTail = (element, requiredFrames = 2, timeout = 5000) => new Promise((resolve) => {
    const startedAt = performance.now();
    let stableFrames = 0;
    const sample = () => {
      const stable = element.dataset.scrollMode === "tail-follow"
        && element.scrollHeight - element.scrollTop - element.clientHeight <= 4
        && visibleRows(element).length > 0;
      stableFrames = stable ? stableFrames + 1 : 0;
      if (stableFrames >= requiredFrames || performance.now() - startedAt >= timeout) {
        resolve();
        return;
      }
      requestAnimationFrame(sample);
    };
    requestAnimationFrame(sample);
  });

  // A bare "timed out" cannot distinguish a missing rail, a stuck jump mask,
  // or a jump-bottom button that never appears on a hosted runner, so every
  // internal gate reports the live transcript state when it fails.
  const describeTranscriptState = (element) => {
    const overlay = document.querySelector(".transcript-navigation-overlay");
    const loadedTurns = [...document.querySelectorAll(".jump-item[data-loaded='true']")]
      .map((marker) => Number(marker.getAttribute("data-turn")))
      .filter(Number.isFinite);
    return JSON.stringify({
      rows: Number.parseInt(element?.dataset.transcriptRowCount ?? "0", 10),
      markers: document.querySelectorAll(".jump-item").length,
      earliestTurn: loadedTurns.length > 0 ? Math.min(...loadedTurns) : null,
      overlayPhase: overlay ? overlay.getAttribute("data-question-jump-phase") ?? "present" : null,
      shellBusy: document.querySelector(".transcript-shell")?.getAttribute("aria-busy") ?? null,
      mode: element?.dataset.scrollMode ?? "missing",
      top: element ? Math.round(element.scrollTop) : null,
      height: element?.scrollHeight ?? null,
      bottomDistance: element
        ? Math.round(element.scrollHeight - element.scrollTop - element.clientHeight)
        : null,
    });
  };

  const loadHistoryRows = async (element, minimumRows) => {
    const rowCount = () => Number.parseInt(element.dataset.transcriptRowCount ?? "0", 10);
    if (rowCount() >= minimumRows) return;
    const earliestLoadedTurn = () => {
      const turns = [...document.querySelectorAll(".jump-item[data-loaded='true']")]
        .map((marker) => Number(marker.getAttribute("data-turn")))
        .filter(Number.isFinite);
      return turns.length > 0 ? Math.min(...turns) : null;
    };
    const rail = await waitFor(() => document.querySelector(".jump-scroll"), 10000)
      .catch(() => {
        throw new Error(`native transcript fixture question rail unavailable: ${describeTranscriptState(element)}`);
      });
    // Target progressively earlier questions through the real navigation UI.
    // A midpoint request usually supplies the 400+ variable-height rows the
    // smoke needs without forcing every archived turn into its first geometry
    // pass at once.
    for (const ratio of [0.65, 0.5, 0.35, 0.2, 0]) {
      if (rowCount() >= minimumRows) return;
      const beforeRows = rowCount();
      const beforeTurn = earliestLoadedTurn();
      const rect = rail.getBoundingClientRect();
      rail.dispatchEvent(new MouseEvent("mousedown", {
        button: 0,
        clientX: rect.left + rect.width / 2,
        clientY: rect.top + rect.height * ratio,
        bubbles: true,
        cancelable: true,
      }));
      // The combined signal-then-mask budget of the original contract gave a
      // native WebView up to 45s to finish a targeted history jump; keep the
      // mask gate itself just as forgiving instead of failing a slow landing.
      await waitFor(() => !document.querySelector(".transcript-navigation-overlay"), 30000)
        .catch(() => {
          throw new Error(`native transcript fixture question jump surface stuck at ratio ${ratio}: ${describeTranscriptState(element)}`);
        });
      // A fixed-size history window keeps the mounted row count constant when
      // an earlier page arrives, and clicking an already-loaded turn changes
      // nothing. Accept an earlier first loaded turn or genuine row growth; a
      // no-op click falls through to the next, earlier ratio instead of
      // stalling the fixture on one signal.
      await waitFor(() => {
        const earliest = earliestLoadedTurn();
        return rowCount() > beforeRows
          || (earliest !== null && (beforeTurn === null || earliest < beforeTurn));
      }, 8000).catch(() => {});
    }
    if (rowCount() >= minimumRows) return;
    throw new Error(`native transcript fixture could not load ${minimumRows} rows (rows=${rowCount()} earliestTurn=${earliestLoadedTurn()})`);
  };

  const start = async () => {
    const topic = await waitFor(() => [...document.querySelectorAll(".project-tree__topic-main")]
      .find((candidate) => candidate.textContent?.includes("bench:tools-38t")));
    topic.click();
    state.phase = "waiting-topic-selection";
    await waitFor(() => (
      document.querySelector(".project-tree__topic--active .project-tree__topic-label")?.textContent?.includes("bench:tools-38t")
        && document.querySelector(".transcript")?.textContent?.includes("pkg-41/mod.go")
    ), 30000);
    state.phase = "waiting-navigation-surface";
    await waitFor(() => !document.querySelector(".transcript-navigation-overlay"), 15000);
    const element = await waitFor(() => {
      const candidate = document.querySelector(".transcript");
      const rows = Number.parseInt(candidate?.dataset.transcriptRowCount ?? "0", 10);
      return candidate instanceof HTMLElement && rows > 0 && candidate.scrollHeight > candidate.clientHeight
        ? candidate
        : null;
    });
    await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    state.phase = "waiting-initial-tail";
    await waitForStableTail(element, 2, 10000);
    state.phase = "waiting-initial-stability";
    // WebView2 can revisit its first 64-row Virtuoso estimate while pending
    // markdown resolves. Two painted stable frames are sufficient before the
    // idempotent history-navigation setup; the loaded 400+ row surface still
    // has to satisfy the stricter tail and geometry gates below before native
    // sampling starts.
    await waitForStableViewport(element, 2, 15000);
    state.phase = "loading-targeted-history";
    await loadHistoryRows(element, 400);
    // When the initial history window already meets the row minimum, no
    // question jump ever ran and the view never left the tail, so the
    // jump-bottom control is correctly absent. Only a genuine jump away from
    // the tail needs the control to return.
    const jumpBottom = await waitFor(() => document.querySelector(".transcript__jump-bottom"), 10000)
      .catch(() => null);
    if (!jumpBottom && (element.dataset.scrollMode !== "tail-follow"
      || element.scrollHeight - element.scrollTop - element.clientHeight > 4)) {
      throw new Error(`native transcript fixture left the tail without a jump-bottom control: ${describeTranscriptState(element)}`);
    }
    jumpBottom?.click();
    state.phase = "waiting-loaded-tail";
    await waitForStableTail(element, 2, 10000);
    // Loaded WebView2 may continue cycling its estimate-only tail by one row;
    // the manual-reader stability gate below remains strict and is the actual
    // prerequisite for starting native input sampling.
    await waitForStableViewport(element, 2, 10000);
    // Position through the product's indexed question navigator. Directly
    // assigning scrollTop while LAST is mounted lets Virtuoso's pending tail
    // range replace the requested history window on WKWebView.
    const rail = await waitFor(() => document.querySelector(".jump-scroll"), 10000);
    state.phase = "waiting-reader-stability";
    for (const ratio of [0.35, 0.2, 0]) {
      const before = element.scrollTop;
      const rect = rail.getBoundingClientRect();
      rail.dispatchEvent(new MouseEvent("mousedown", {
        button: 0,
        clientX: rect.left + rect.width / 2,
        clientY: rect.top + rect.height * ratio,
        bubbles: true,
        cancelable: true,
      }));
      await waitFor(() => (
        Math.abs(element.scrollTop - before) > element.clientHeight / 2
          || document.querySelector(".transcript-navigation-overlay")
      ), 10000);
      await waitFor(() => !document.querySelector(".transcript-navigation-overlay"), 15000);
      await waitForStableViewport(element);
      state.initialDistance = element.scrollHeight - element.scrollTop - element.clientHeight;
      if (state.initialDistance >= element.clientHeight * 2) break;
    }
    if (state.initialDistance < element.clientHeight * 2) {
      throw new Error(`native transcript fixture did not establish a deep reader start (${state.initialDistance}px)`);
    }
    // The navigator is programmatic setup only. Claim manual reader ownership
    // before the host begins the platform-native downward traversal.
    element.dispatchEvent(new WheelEvent("wheel", { deltaY: -1, bubbles: true, cancelable: true }));
    await waitFor(() => element.dataset.scrollMode === "reader-gesture" || element.dataset.scrollMode === "manual", 5000);
    state.phase = "waiting-reader-geometry";
    // A bounded manual-reading window may deliberately mount every row in the
    // current history page. Do not classify that first estimate-to-measurement
    // pass as a stable native extent: wait until the whole bounded page is
    // mounted and the painted extent is quiet before establishing the smoke
    // baseline. Pending Markdown remains part of the streamed test itself; a
    // later collapse during native input still fails against the unchanged
    // max-extent gate.
    const logicalRows = Number.parseInt(element.dataset.transcriptRowCount ?? "0", 10);
    await waitFor(() => (
      element.querySelectorAll(".transcript__row[data-index]").length >= logicalRows
    ), 30000);
    await waitForStableViewport(element, 8, 15000);
    const virtualList = element.querySelector(".transcript__virtual-sizer");
    const footer = virtualList?.nextElementSibling;
    if (!(footer instanceof HTMLElement)) throw new Error("native transcript fixture footer is unavailable");
    const growthSurface = document.createElement("div");
    growthSurface.setAttribute("aria-hidden", "true");
    growthSurface.style.cssText = "height:0;width:100%;pointer-events:none;";
    footer.append(growthSurface);
    state.transcript = element;
    state.frames = [];
    state.active = true;
    state.growthTicks = 0;
    state.growthSurface = growthSurface;
    state.wheelEvents = 0;
    state.wheelDelta = 0;
    state.wheelInsideTranscript = 0;
    state.wheelMaxDelta = 0;
    state.phase = "ready";
    state.growthTimer = window.setInterval(growFooter, 16);
    scheduleSample();
    const point = outerReaderPoint(element);
    if (!point) throw new Error("native transcript outer reader target is unavailable");
    post({
      type: "ready",
      rows: Number.parseInt(element.dataset.transcriptRowCount ?? "0", 10),
      top: element.scrollTop,
      point,
    });
  };

  const finish = async () => {
    window.clearInterval(state.growthTimer);
    const element = state.transcript;
    if (element instanceof HTMLElement) await waitForStableTail(element);
    state.active = false;
    const frames = state.frames;
    let maxReverse = 0;
    let rawMaxReverse = 0;
    let worstReverse = null;
    for (let index = 1; index < frames.length; index += 1) {
      const previous = frames[index - 1];
      const current = frames[index];
      const rawReverse = previous.top - current.top;
      rawMaxReverse = Math.max(rawMaxReverse, rawReverse);
      const currentTops = new Map(current.visible.map((row) => [row.index, row.top]));
      const visibleDeltas = previous.visible
        .filter((row) => row.index && currentTops.has(row.index))
        .map((row) => currentTops.get(row.index) - row.top)
        .sort((left, right) => left - right);
      // scrollTop can decrease when a measured extent above the viewport
      // contracts even though the same rows remain visually stationary. The
      // user-visible reverse displacement is the median screen movement of
      // rows painted in both adjacent frames; raw scrollTop remains attached
      // to the failure record for range diagnostics.
      const reverse = visibleDeltas.length > 0
        ? visibleDeltas[Math.floor(visibleDeltas.length / 2)]
        : rawReverse;
      if (reverse > maxReverse) {
        maxReverse = reverse;
        worstReverse = { previous, current, rawReverse, commonRows: visibleDeltas.length };
      }
    }
    const firstTop = frames[0]?.top ?? 0;
    const lastTop = frames.at(-1)?.top ?? firstTop;
    const blankFrames = frames.filter((frame) => !frame.occupied);
    const distance = element instanceof HTMLElement
      ? element.scrollHeight - element.scrollTop - element.clientHeight
      : Number.POSITIVE_INFINITY;
    const viewportRect = element instanceof HTMLElement ? element.getBoundingClientRect() : null;
    const footerRect = state.growthSurface?.parentElement?.getBoundingClientRect();
    const initialScrollHeight = frames[0]?.height ?? element?.scrollHeight ?? 0;
    const minScrollHeight = Math.min(initialScrollHeight, ...frames.map((frame) => frame.height));
    const maxScrollHeight = Math.max(initialScrollHeight, ...frames.map((frame) => frame.height));
    const finalScrollHeight = element?.scrollHeight ?? frames.at(-1)?.height ?? 0;
    const collapseTolerance = Math.max(96, (element?.clientHeight ?? 0) * 0.5);
    const mountedCoverage = frames.length > 0 ? (frames.length - blankFrames.length) / frames.length : 0;
    const result = {
      type: "result",
      passed: frames.length >= 20
        && lastTop > firstTop + 96
        && maxReverse <= 96
        && frames.every((frame) => frame.occupied)
        && state.initialDistance >= (element?.clientHeight ?? Number.POSITIVE_INFINITY) * 2
        && distance <= 4
        && element?.dataset.scrollMode === "tail-follow"
        && finalScrollHeight >= maxScrollHeight - collapseTolerance,
      rows: Number.parseInt(element?.dataset.transcriptRowCount ?? "0", 10),
      frames: frames.length,
      firstTop,
      lastTop,
      initialDistance: state.initialDistance,
      maxReverse,
      rawMaxReverse,
      worstReverse,
      occupied: frames.every((frame) => frame.occupied),
      blankFrames: blankFrames.length,
      firstBlank: blankFrames[0] ?? null,
      distance,
      mode: element?.dataset.scrollMode ?? "missing",
      growthTicks: state.growthTicks,
      initialScrollHeight,
      minScrollHeight,
      maxScrollHeight,
      finalScrollHeight,
      initialScrollTop: firstTop,
      finalScrollTop: lastTop,
      totalFrames: frames.length,
      mountedCoverage,
      finalBottomDistance: distance,
      finalMode: element?.dataset.scrollMode ?? "missing",
      deliveredNativeEvents: state.wheelEvents,
      wheelEvents: state.wheelEvents,
      wheelDelta: state.wheelDelta,
      wheelInsideTranscript: state.wheelInsideTranscript,
      wheelMaxDelta: state.wheelMaxDelta,
      paddingBottom: element instanceof HTMLElement ? Number.parseFloat(getComputedStyle(element).paddingBottom) : null,
      footerBottomDistance: viewportRect && footerRect ? footerRect.bottom - viewportRect.bottom : null,
      writes: state.writes.slice(-20),
    };
    post(result);
    return result;
  };

  const finishMicro = () => {
    window.clearInterval(state.growthTimer);
    state.active = false;
    const element = state.transcript;
    const frames = state.frames;
    const blankFrames = frames.filter((frame) => !frame.occupied);
    const heights = frames.map((frame) => frame.height);
    const result = {
      type: "result",
      passed: state.wheelEvents > 0 && frames.length > 0,
      rows: Number.parseInt(element?.dataset.transcriptRowCount ?? "0", 10),
      frames: frames.length,
      totalFrames: frames.length,
      firstTop: frames[0]?.top ?? 0,
      lastTop: frames.at(-1)?.top ?? 0,
      initialScrollTop: frames[0]?.top ?? 0,
      finalScrollTop: frames.at(-1)?.top ?? 0,
      initialScrollHeight: heights[0] ?? element?.scrollHeight ?? 0,
      minScrollHeight: heights.length > 0 ? Math.min(...heights) : element?.scrollHeight ?? 0,
      maxScrollHeight: heights.length > 0 ? Math.max(...heights) : element?.scrollHeight ?? 0,
      finalScrollHeight: element?.scrollHeight ?? heights.at(-1) ?? 0,
      blankFrames: blankFrames.length,
      mountedCoverage: frames.length > 0 ? (frames.length - blankFrames.length) / frames.length : 0,
      finalBottomDistance: element instanceof HTMLElement ? element.scrollHeight - element.scrollTop - element.clientHeight : 0,
      finalMode: element?.dataset.scrollMode ?? "missing",
      deliveredNativeEvents: state.wheelEvents,
      mode: element?.dataset.scrollMode ?? "missing",
      occupied: blankFrames.length === 0,
    };
    post(result);
    return result;
  };

  window.__reasonixNativeTranscriptSmoke = { start, finish, finishMicro };
  start().catch((error) => {
    const message = String(error?.message ?? error);
    post({ type: "error", message: `${message} (${state.phase})`, phase: state.phase });
  });
})();
