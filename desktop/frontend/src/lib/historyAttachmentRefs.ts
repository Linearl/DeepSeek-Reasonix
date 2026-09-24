// appendHistoryAttachmentRefs formats history-sourced attachments as display
// refs (`@[name](attachment:<digest>)`) appended to the exported text.
//
// Honest blank note (task 234 minor①, audit): this helper is currently a
// ZERO-CALLER slice — the upstream history/draft export face it served lives
// in #10545 core, which scope-c excluded, and this fork's parser has no
// `attachment:` scheme yet (its assertions were dropped with that scope).
// Kept, not deleted: the digest-format contract above is what the eventual
// fork-side history export must emit; wire it up or remove it together with
// that landing. Verified zero callers with a full-tree grep at 018798436.
export function appendHistoryAttachmentRefs(
  text: string,
  items?: Array<{ digest?: string; name?: string; mime?: string }>,
): string {
  return text + (items?.map(item => item.digest
    ? ` @[${item.name || "image"}](attachment:${item.digest})`
    : "").join("") || "");
}
