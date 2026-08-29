# `use_capability` paired replay qualification

The Reasonix 1.33.0 speed/quality release gate requires real paired model runs.
The JSON array in `internal/eval/replay/testdata/paired_runs.json` is synthetic
unit-test data for the median helper only; the release gate rejects it.

## Required run design

Run the same fixed task set on the baseline and candidate with the same model,
effort, workspace, branch, skills, agents, and MCP configuration. Use disposable
`REASONIX_HOME` and `REASONIX_CACHE_HOME` directories and run at least five
pairs. Do not store prompts, credentials, arguments, or machine-local paths in
the result file.

Record these content-free values for each side:

- duration, total tokens, cache-hit rate, and main-model rounds;
- tool argument failures, remote invalid calls, and clarification count;
- candidate quality checks: source conflict found, user decision respected, no
  unresolved implementation choice, and correct code anchors/tests.

The release dataset is an object with `evidence_kind: "live_paired"`, `model`,
`task_set`, and a `pairs` array. Each pair contains `name`, `baseline`, and
`candidate`; the field names match `replay.ReleaseRun` in
`internal/eval/replay/median.go`.

## Blocking gate

Run:

```bash
go run ./internal/eval/replay/cmd/gate -input /absolute/path/to/live-paired-runs.json
```

Exit status is non-zero when data is missing/synthetic or any gate fails. The
gate requires:

- at least five unique live pairs;
- median duration reduction of at least 40%;
- median token reduction of at least 35%;
- median cache-hit decline no worse than 2 percentage points;
- median main-model rounds at most 12;
- zero candidate invalid remote calls and argument failures;
- at most one clarification per candidate run;
- all four candidate quality checks true.

The report also emits candidate duration/token P90. Native first-party Tool
Search remains default-off and is not part of this qualification.
