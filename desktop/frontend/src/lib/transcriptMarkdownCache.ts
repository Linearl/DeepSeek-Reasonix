import type { MarkdownBlock } from "./markdownPipeline";

export interface ParsedMarkdownValue {
  source: string;
  blocks: MarkdownBlock[];
  selectionText: string;
  selectionRevision: number;
  /** Source/projection UTF-16 bytes plus the estimated HAST weight. */
  bytes: number;
}

/** Byte-bounded LRU whose active selection entries may be pinned. */
export class TranscriptMarkdownCache {
  private readonly entries = new Map<string, { value: ParsedMarkdownValue; bytes: number }>();
  private readonly pins = new Map<string, number>();
  bytes = 0;
  evictions = 0;

  constructor(readonly budgetBytes: number) {}

  /**
   * Cache key: the revision alone.
   *
   * The parsed value is a pure function of the source text - the worker takes
   * nothing else, and `revision` is already an FNV-1a fingerprint of that text - so
   * the revision identifies the entry by itself. `entryId` stays in the signatures
   * for callers, but keying on it only loses hits: the same message carries
   * `item.id` while live and `he:<entryId>` once it is history, and remote/serve
   * hydration gives it a per-load `h<seq>` id, so an identity-keyed cache misses on
   * every one of those transitions even though the text never changed.
   *
   * Two different messages with identical text therefore share one entry, which is
   * correct - identical text parses identically - and the `source` comparison on
   * read still guards against a hash collision.
   */
  private key(_entryId: string | undefined, revision: number): string {
    return `md:${revision}`;
  }

  get(entryId: string | undefined, revision: number): ParsedMarkdownValue | undefined {
    const key = this.key(entryId, revision);
    const entry = this.entries.get(key);
    if (!entry) return undefined;
    this.entries.delete(key);
    this.entries.set(key, entry);
    return entry.value;
  }

  set(entryId: string | undefined, revision: number, value: ParsedMarkdownValue): void {
    const key = this.key(entryId, revision);
    const previous = this.entries.get(key);
    if (previous) this.bytes -= previous.bytes;
    const bytes = Math.max(0, value.bytes);
    this.entries.set(key, { value, bytes });
    this.bytes += bytes;
    this.enforceBudget();
  }

  pin(entryId: string | undefined, revision: number): () => void {
    const key = this.key(entryId, revision);
    this.pins.set(key, (this.pins.get(key) ?? 0) + 1);
    let released = false;
    return () => {
      if (released) return;
      released = true;
      const count = this.pins.get(key) ?? 0;
      if (count <= 1) this.pins.delete(key);
      else this.pins.set(key, count - 1);
      this.enforceBudget();
    };
  }

  size(): number {
    return this.entries.size;
  }

  private enforceBudget(): void {
    while (this.bytes > this.budgetBytes && this.entries.size > 1) {
      const victimKey = Array.from(this.entries.keys()).find((key) => !this.pins.has(key));
      if (!victimKey) break;
      const victim = this.entries.get(victimKey);
      if (victim) this.bytes -= victim.bytes;
      this.entries.delete(victimKey);
      this.evictions += 1;
    }
  }
}
