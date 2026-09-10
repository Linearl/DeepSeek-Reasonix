import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { transcriptRowMeasurementVersion, type TranscriptRow } from "../lib/transcriptRows";

// The version only reads item content, so these rows carry the minimum the
// switch in transcriptRowMeasurementVersion looks at. Item objects are built
// separately where a test needs to hold one by reference.
function answerRow(item: object, key = "a:u1"): TranscriptRow {
  return { kind: "answer", key, layoutVariant: "text-flow", item: item as never } as unknown as TranscriptRow;
}

function userItem(id: string, text: string): object {
  return { kind: "user", id, text };
}

function toolItem(id: string, output: string): object {
  return { kind: "tool", id, name: "bash", output };
}

describe("transcript row measurement version", () => {
  it("returns an equal version for repeated reads of one row", () => {
    const row = answerRow(toolItem("t1", "x".repeat(10_000)));
    assert.equal(transcriptRowMeasurementVersion(row), transcriptRowMeasurementVersion(row));
  });

  it("derives the version from content, not from object identity", () => {
    // Row blocks are rebuilt whenever the transcript changes, and streaming
    // rebuilds them again on every chunk. Measurement reuse depends on equal
    // content producing an equal version across distinct objects.
    assert.equal(
      transcriptRowMeasurementVersion(answerRow(userItem("u1", "same"))),
      transcriptRowMeasurementVersion(answerRow(userItem("u1", "same"))),
    );
  });

  it("changes the version when the payload changes", () => {
    assert.notEqual(
      transcriptRowMeasurementVersion(answerRow(toolItem("t1", "first"))),
      transcriptRowMeasurementVersion(answerRow(toolItem("t1", "second"))),
    );
    assert.notEqual(
      transcriptRowMeasurementVersion(answerRow(userItem("u1", "first"))),
      transcriptRowMeasurementVersion(answerRow(userItem("u1", "second"))),
    );
  });

  it("keeps an unchanged item's version stable while its row is rebuilt", () => {
    // This is the streaming shape: the controller keeps untouched items by
    // reference and replaces only the one that changed, while the rows around
    // them are rebuilt as fresh objects. A reused item may not be re-hashed
    // into a different version, and a changed one may not be masked.
    const settledItem = toolItem("t1", "y".repeat(8_000));
    const before = transcriptRowMeasurementVersion(answerRow(settledItem));

    assert.equal(transcriptRowMeasurementVersion(answerRow(settledItem)), before);

    const changed = answerRow(toolItem("t1", `${"y".repeat(8_000)}tail`));
    assert.notEqual(transcriptRowMeasurementVersion(changed), before);
  });

  it("keeps key-only rows free of item hashing", () => {
    const older = { kind: "older-history", key: "older-history" } as unknown as TranscriptRow;
    const actions = { kind: "turn-actions", key: "ta:u1" } as unknown as TranscriptRow;
    assert.equal(transcriptRowMeasurementVersion(older), "0:0");
    assert.equal(transcriptRowMeasurementVersion(actions), "0:0");
  });

  it("hashes every item of a grouped row", () => {
    const group = (items: object[]): TranscriptRow =>
      ({ kind: "tool-group", key: "tg:1", items, layoutVariant: "static" }) as unknown as TranscriptRow;
    const first = group([toolItem("a", "1"), toolItem("b", "2")]);
    const same = group([toolItem("a", "1"), toolItem("b", "2")]);
    const changed = group([toolItem("a", "1"), toolItem("b", "3")]);
    assert.equal(transcriptRowMeasurementVersion(first), transcriptRowMeasurementVersion(same));
    assert.notEqual(transcriptRowMeasurementVersion(first), transcriptRowMeasurementVersion(changed));
  });
});
