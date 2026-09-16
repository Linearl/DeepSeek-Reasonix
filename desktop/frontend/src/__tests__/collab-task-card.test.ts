// Run: tsx src/__tests__/collab-task-card.test.ts

import { parseCollabTaskCard, collabCardIsFailed, collabCardNodeLabel } from "../lib/collabTaskCard";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\nCollab task card parsing");

const full = JSON.stringify({
  id: "card_1",
  title: "Ship collab base",
  status: "running",
  initiatorContactId: "sc_a",
  assigneeContactId: "sc_b",
  body: "land 141-145",
  nodes: [{ contactId: "sc_b", role: "expert", note: "started" }],
  hop: 1,
});
const card = parseCollabTaskCard(full);
ok(card !== null && card.title === "Ship collab base", "parses a complete card");
ok(card?.status === "running", "keeps a valid status");
ok(card?.nodes.length === 1 && collabCardNodeLabel(card.nodes[0]) === "sc_b", "keeps the node chain");
ok(card !== null && collabCardIsFailed(card) === false, "a running card is not failed");

// A failed card must be recognisable even when the status field lags behind.
const failure = parseCollabTaskCard(JSON.stringify({ id: "c", title: "t", status: "running", error: "target refused" }));
ok(failure !== null && collabCardIsFailed(failure) === true, "an error makes the card read as failed");

// Unknown statuses must not blank the card; they fall back to pending.
const odd = parseCollabTaskCard(JSON.stringify({ id: "c", title: "t", status: "weird" }));
ok(odd?.status === "pending", "unknown status falls back to pending");

// Malformed input must fail closed to the code viewer rather than render an
// empty-but-plausible card.
ok(parseCollabTaskCard("not json") === null, "non-JSON falls back");
ok(parseCollabTaskCard("[]") === null, "an array is not a card");
ok(parseCollabTaskCard(JSON.stringify({ title: "no id" })) === null, "a card without id falls back");
ok(parseCollabTaskCard(JSON.stringify({ id: "only-id" })) === null, "a card without title falls back");
ok(parseCollabTaskCard("   ") === null, "empty block falls back");

// Present-but-wrong-typed fields must not crash the card.
const sloppy = parseCollabTaskCard(JSON.stringify({ id: "c", title: "t", nodes: "nope", hop: "two" }));
ok(sloppy !== null && sloppy.nodes.length === 0 && sloppy.hop === undefined, "wrong-typed fields are ignored");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
