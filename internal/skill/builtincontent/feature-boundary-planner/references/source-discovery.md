# Current Source Discovery

Paths below are repository-relative starting points, not a feature inventory. Confirm each path exists and contains tracked source before using it. Discover exact symbols and callers from the current checkout.

Use [reasonix-feature-graph.yaml](reasonix-feature-graph.yaml) for known capability aliases, UI surfaces, owners and ranked relationships. Search first rather than loading the whole graph:

```sh
git grep -n -e 模型选择 -e 消息队列 -e 实验室 -- internal/skill/builtincontent/feature-boundary-planner/references/reasonix-feature-graph.yaml
```

Read the matching node and relationships referencing its ID, then inspect the declared source. Missing graph coverage is a reason to use the source entrypoints below, not evidence that a feature is absent. File and symbol presence validates a retrieval seed, not its behavior or test coverage.

| Concern               | Starting points                                                                 | Evidence to trace                                                     |
| --------------------- | ------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| Desktop React UI      | `desktop/frontend/src` (`App.tsx`, `components/`, `lib/`, `store/`, `lab/`)     | entrypoint, draft owner, shared callers, validation, commit action    |
| Wails app bindings    | `desktop/app*.go`, generated `desktop/frontend/wailsjs/`                        | bound method, argument shape, event emission                          |
| Session/topic runtime | `desktop/session_*.go`, `desktop/topic_*.go`, `desktop/tabs*.go`                | authoritative owner, admission, projection, persistence               |
| Turn pipeline         | `desktop/turn_admission.go`, `desktop/turn_submission_app.go`                   | admission gate, submission commands, idempotency, recovery            |
| Agent runtime & tools | `internal/agent`, `internal/tool`, `internal/tool/builtin`                       | tool contract, permission gates, execution environment                |
| Skills                | `internal/skill`, `internal/skill/builtincontent`                                | builtin vs user-dir override, invocation path, catalog rendering      |
| Settings & lab        | `desktop/settings_app.go`, `desktop/frontend/src/lab/`                          | option source, default, gating (lab switch), persistence             |
| Collaboration         | `desktop/session_collab*.go`, `internal/collabchannel`, `internal/collabinbox`  | channel contract, inbox admission, heartbeat schedule                 |
| Model/provider config | `desktop/model_settings.go`, `desktop/app_model.go`, `internal/config`          | per-tab model, provider entry, probe, persisted config                |
| Persistence           | `desktop/topic_state_store.go`, `desktop/session_storage_mode.go`, `desktop/pinned_context_store.go` | schema, write/read path, migration |
| Module boundaries     | `architecture-policy.json`, `architecture-baseline.json`                        | declared roots, layers, dependency baseline                           |

Start with bounded searches in the relevant area:

```sh
git ls-files desktop/frontend/src/lib | grep -i composer
git grep -n "workspaceIdentity\|clientMode" -- desktop internal
```

Choose search terms from the user's behavior and the discovered source. Inspect scripts/commands (`desktop/frontend/package.json`, `Makefile`, `go.mod`) before invoking development or test commands. An unavailable tool or runner must be reported as unavailable rather than replaced with an invented command.

For each stateful path, record:

| Question                                          | Evidence                                               |
| ------------------------------------------------- | ------------------------------------------------------ |
| Who accepts the write?                            | command handler and owning service/runtime             |
| Which surfaces read it?                           | callers, hooks/store subscriptions and projections     |
| What persists?                                    | repository/config schema and actual write/read paths   |
| What happens after reconnect or stale completion? | sequence/identity guards and recovery handlers         |
| What proves the behavior?                         | executed test or observed runtime path with assertions |

For affected existing tests, preserve their case identity and distinguish test existence from a successful run. For new cases, specify setup, action, assertions, environment and required evidence before choosing a runner.
