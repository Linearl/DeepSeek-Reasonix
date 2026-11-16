// Shared CLI helpers for the release-tooling scripts (tl1626 档B).
// Extracted verbatim from the copies that lived inline in release-notes.mjs,
// release-event.mjs, and generate-release-notes.mjs so a single change
// (e.g. supporting "--flag=value") lands once instead of 2-3 times.
// Keep behavior byte-identical with the originals; the only semantic knob is
// the `command` option of parseArgs (see below).

export function invariant(condition, message) {
  if (!condition) throw new Error(message);
}

// Parse an argv tail (already stripped of the program name) into { key: value }
// from "--key value" pairs. Two historical forms are preserved:
//   parseArgs(argv, { command: "validate" })
//     - first slot is the command word, defaulting to "validate" when absent;
//     - the release-notes.mjs / release-event.mjs form.
//   parseArgs(argv)
//     - every slot is a flag pair; no command word;
//     - the generate-release-notes.mjs form.
// A trailing "--flag" without a value stores `undefined` for that key,
// exactly as the originals did.
export function parseArgs(argv, { command = undefined } = {}) {
  if (command === undefined) {
    const values = {};
    for (let index = 0; index < argv.length; index += 1) {
      const arg = argv[index];
      if (!arg.startsWith("--")) throw new Error(`unexpected argument ${arg}`);
      values[arg.slice(2)] = argv[++index];
    }
    return values;
  }
  const [first = command, ...rest] = argv;
  const values = { command: first };
  for (let index = 0; index < rest.length; index += 1) {
    const arg = rest[index];
    if (!arg.startsWith("--")) throw new Error(`unexpected argument ${arg}`);
    values[arg.slice(2)] = rest[++index];
  }
  return values;
}
