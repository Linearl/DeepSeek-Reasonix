// Task 400 (upstream #11272 -> #11282): the history failure paths replaced the
// reader's own error with a fixed sentence, so the banner's Details repeated
// the headline and a broken session (permission, corruption, version skew)
// could never be diagnosed from the UI. The localized summary stays the
// headline; the reader's reason rides along through a caller-supplied
// localized detail template.
/** The reader's own failure text, or "" when the cause carries nothing showable. */
export function hydrateFailureReason(cause: unknown): string {
  if (typeof cause === "string") return cause.trim();
  if (cause instanceof Error) return cause.message.trim();
  return "";
}

/**
 * Banner text for a failed history read: the localized summary alone when the
 * cause says nothing (never a dangling separator), otherwise the localized
 * detail template filled with the reason. `format` is injected so this module
 * stays free of i18n imports and the wrapper copy lives in the locale files.
 */
export function hydrateFailureDetail(
  summary: string,
  cause: unknown,
  format?: (reason: string) => string,
): string {
  const reason = hydrateFailureReason(cause);
  if (!reason || reason === summary) return summary;
  return format ? format(reason) : `${summary} ${reason}`;
}

/** Preserve transcript on failed hydrate instead of painting a successful empty session. */
export function applyHydrateErrorState<
  TItem,
  S extends {
    items: TItem[];
    hydratePlaceholderItems?: TItem[];
    hydrateHistoryLoaded?: boolean;
  },
>(s: S, reason: string, error: string): S & {
  hydrating: false;
  hydrateReason: string;
  hydrateError: string;
  hydrateHistoryLoaded?: boolean;
  hydratePlaceholderItems: undefined;
} {
  const keptItems = s.items.length > 0
    ? s.items
    : (s.hydratePlaceholderItems?.length ? s.hydratePlaceholderItems : s.items);
  return {
    ...s,
    items: keptItems,
    hydrating: false,
    hydrateReason: reason,
    hydrateError: error,
    hydrateHistoryLoaded: s.hydrateHistoryLoaded || keptItems.length > 0 || undefined,
    hydratePlaceholderItems: undefined,
  };
}

export function hydratePlaceholderItems<TItem>(
  optionsItems: TItem[] | undefined,
): TItem[] | undefined {
  return optionsItems?.length ? optionsItems : undefined;
}
