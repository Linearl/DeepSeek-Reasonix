# GC session takeover protocol

<a href="../README.md">README</a>
&nbsp;·&nbsp;
<a href="./GC_SESSION_TAKEOVER.zh-CN.md">简体中文</a>
&nbsp;·&nbsp;
<a href="./GUIDE.md">General guide</a>

The wire contract behind "take over a conversation from the phone"
(GrandCouncil → reasonix gateway → pooled serve → desktop). This document is
the authoritative description of the takeover request/response contract, the
202 polling convention, and the window constants. Source tasks: 36/249/539,
tl1854 (R1–R5); the design-gap handoff that drove tl1854 is
`GC-远程接管设计缺口-交接reasonix-20261127` (GC workspace).

## Contents

- [Actors](#actors)
- [The takeover request](#the-takeover-request)
- [Responses: 204 / 202 / 409](#responses-204--202--409)
- [Polling a 202 to its terminal state](#polling-a-202-to-its-terminal-state)
- [Windows and constants](#windows-and-constants)
- [Session-level ownership (tl1854 R5)](#session-level-ownership-tl1854-r5)
- [What the confirmation dialog is not](#what-the-confirmation-dialog-is-not)
- [Reclaim and release](#reclaim-and-release)

## Actors

```
GrandCouncil (phone) ── gateway token ──▶ servepool gateway (desktop process)
                                             │ takeover gate (prompt/veto)
                                             ▼
                                     pooled serve (per project)
                                             │ lease / marker protocol
                                             ▼
                                     desktop tabs (lease holders)
```

- The **gateway token** is the access control. Everything below assumes a
  caller that already holds it.
- The **serve** is the per-project runtime that holds session leases.
- The **desktop gate** prompts the desktop user only when a desktop tab
  actually holds the target session's lease.

## The takeover request

`POST /p/<project>/takeover-session` with the gateway token:

```json
{"name": "<session name>", "from": "<device id>", "force": false}
```

- `name`: bare transcript stem (validated with full containment on the serve).
- `from`: the caller's device identity, used for the desktop notice and the
  write-authority observer.
- `force` (`force: true`): the pre-existing forced channel — no desktop
  prompt, the desktop turn is cancelled and the session yields immediately.
  Semantics are unchanged by tl1854.

Before the request the client normally `POST /resume`d the session, which
makes the serve itself hold the session lease.

## Responses: 204 / 202 / 409

| Status | Meaning | Body |
| --- | --- | --- |
| `204` | Takeover complete. Either the serve acquired the lease synchronously, or the serve already held it itself (idle session — no desktop holder to yield, no prompt on the desktop). | none |
| `202` | The desktop accepted (or its veto window expired without a rejection), but its turn is still running: the takeover finishes in the background. | `{"status":"pending","session":"<name>","pollWindowSeconds":30,"vetoWindowSeconds":10}` plus `Retry-After: 5` |
| `409` | Hard refusal — the desktop user clicked 拒绝 (or the lease was lost in a release race). Nothing auto-allows after a 409 rejection. | plain text message |
| `400/404` | Validation / unknown session. | plain text |

The desktop prompt is a **veto window, not a confirmation** (tl1854 R3): if
nobody at the desktop answers within `vetoWindowSeconds`, the takeover
proceeds through the same non-interrupting yield path as an explicit accept.
A running desktop turn is never cancelled by this path (only `force` does).

## Polling a 202 to its terminal state

Poll `GET /p/<project>/status?session=<transcript path>` (the full transcript
path as listed by `GET /sessions`, not the bare name):

- `takeoverPending: true` — still waiting for the desktop turn to finish.
- `takeoverStatus: "failed"` with `takeoverMessage` — terminal failure
  (desktop withdrew the request, or the yield window expired without a
  yield: "desktop 本轮未结束，可稍后重试").
- neither pending nor failed — **done**; the success signal is the view's
  regular `takenOver`/`running` fields.

Every `?session=` status view carries these fields (all three view branches
decorate them; tl1854 R1 closed the one branch that skipped the decoration),
including the main remote-takeover branch where the target session is the
serve's own bound session. Terminal outcomes stay queryable for 10 minutes
(`takeoverAttemptTTL`).

**Ordering invariant** ("both absent = done" is safe): the serve records the
pending attempt *before* it writes the 202 (`beginTakeoverAttempt` precedes
the response). A client that starts polling after receiving the 202 can never
observe "no fields" as a false completion.

## Windows and constants

| Window | Value | Where defined | Purpose |
| --- | --- | --- | --- |
| `pollWindowSeconds` | 30 s | `agent.SessionTakeoverYieldWindow` | How long the serve waits for the desktop turn to drain after acceptance |
| `vetoWindowSeconds` | 10 s | `agent.SessionTakeoverVetoWindow` | How long the desktop dialog waits for a veto; expiry = allow |

Both ride the 202 body so clients never hardcode them. The desktop dialog
countdown reads the same constant through the `app:takeover-request` event
(`veto_window_ms`), so the two ends cannot drift.

## Session-level ownership (tl1854 R5)

A takeover/release affects exactly ONE conversation, not the device:

- The submit guard refuses other identities only for the session a live
  heartbeat still covers (previously: any live device heartbeat locked the
  whole instance).
- Heartbeat `release` with `name` releases only that session; without `name`
  the whole device record is released (legacy clients).
- Session entries expire on their own timestamps; a device record lives as
  long as any of its sessions is fresh.
- The desktop releases leases only for the tabs of the taken-over session;
  other sessions keep working on the desktop.

Known limits (unchanged): one pooled serve has one foreground session, so
multiple remote clients sharing one instance still displace each other's
foreground; and the confirmation dialog is not a security boundary (below).

## What the confirmation dialog is not

The desktop dialog is a courtesy notice, not an access boundary: write
authority is granted by heartbeats, and `/heartbeat` does not pass the
desktop gate — any client holding the gateway token can hold write authority.
The **gateway token is the access control**. Do not treat the dialog as a
security decision, and do not build authorization logic on top of it.

## Reclaim and release

- `POST /p/<project>/release-session` `{"name": "..."}` — the serve releases
  its own lease (with an optional handoff reservation) so the desktop can
  take the session back; refuses while a turn is running.
- `POST /reclaim` — take a mirrored/held session back after a local writer
  adopted it.
- Device lease: `POST /heartbeat` keeps it alive (15 s cadence recommended);
  `release: true` (optionally with `name`, see above) releases immediately;
  90 s without a beat expires it and frees the held session.
