#!/usr/bin/env node
// Class contract — every className referenced by JSX must have a CSS definition.
//
// Why: task 578 (2026-10-07) found 213 class names referenced by JSX but defined
// nowhere in the CSS (32 of them on buttons). The existing checks cannot catch
// this: check-theme-token-contract only validates var(--token) references,
// check-css-syntax / check-duplicate-definitions never cross JSX against CSS.
//
// Four assertions:
//   1. class-name diff: (CSS definitions ∪ TSX inline <style> definitions) vs
//      className string literals. Any referenced-but-undefined class fails,
//      except entries in BASELINE_WHITELIST (pre-existing debt — only NEW
//      unstyled references are blocked; do not grow the list).
//   2. Tailwind-shaped classes: this project has no Tailwind (no config, no
//      dependency, no directives — verified 2026-10-07), so any Tailwind-
//      pattern class is dead weight that styles nothing (PinnedFilesShelf
//      incident). Known legacy files are exempted; new ones fail.
//   3. hardcoded colors: color-ish declarations with #hex/rgb(/hsl( and no
//      var() bypass the theme token system (group D of the same audit).
//      Existing sites are whitelisted pending design decisions; new ones fail.
//   4. exemptions: template-literal classNames (`btn--${variant}`) are dynamic
//      and never enter the referenced set; __tests__/** and *.test.* files are
//      skipped entirely.
//
// Usage (from desktop/frontend):
//   node scripts/check-class-contract.mjs               # contract check, exit 1 on violation
//   node scripts/check-class-contract.mjs --emit-baseline   # print current diff sets as JSON
//   node scripts/check-class-contract.mjs --self-test       # negative tests: must be blocked

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(scriptDir, "..");
const sourceRoot = path.join(frontendRoot, "src");

// ── Whitelists (存量债务，只出不进；新引用必须配真实定义) ──────────────────
// Assertion 1: pre-existing referenced-but-undefined classes at the 578 tier-1
// landing. Regenerate with --emit-baseline if a deliberate mass-change happens;
// every removal needs a real definition, never a silent edit.
const BASELINE_WHITELIST = new Set([
// 206 entries emitted by --emit-baseline at the task-578 tier-1 landing
// (2026-10-07, baseline 2dc519eb7 + tier-1 CSS). Pre-existing debt from the
// audit; do not add entries — a new reference needs a real definition.
  "app-chrome__tab-strip--darwin",
  "app-chrome__tab-strip--native",
  "appearance-overview__segmented--text-size",
  "appearance-overview__segmented--theme",
  "badge--",
  "badge--neutral",
  "bg-surface-raised/40",
  "border-b",
  "border-border/40",
  "bot-detail-section--runtime-primary",
  "bot-detail__panel--access",
  "bot-detail__panel--facts",
  "bot-gateway-panel",
  "bot-global-access-panel",
  "bot-routes-panel",
  "bot-secret-row--dingtalk",
  "cap-mcp-field--name",
  "cap-mcp-field--transport",
  "cap-plugin-fields--git",
  "cap-plugin-fields--local",
  "cap-skill-badge--builtin",
  "cap-skill-policy",
  "cap-source__side",
  "cap-source__switch",
  "capsule-panel__elapsed",
  "chat-older",
  "collab-card__node",
  "collab-card__title",
  "collab-inbox-panel__dupcount",
  "collab-inbox-panel__note",
  "collab-inbox-panel__revision",
  "collab-inbox-panel__route",
  "compact-ratio-choice__row--custom",
  "compaction--pending",
  "compaction__body",
  "compaction__head",
  "composer-delivery-trigger",
  "composer-inbox-recovery",
  "composer-meta__control--capsule",
  "composer-meta__control--fold",
  "composer-meta__control--target",
  "composer-modebar--split-target",
  "context-panel__mcp-list",
  "context-panel__section-title",
  "context-ring-popover__turn-metrics",
  "diag-section--frontend",
  "diff__line--new",
  "diff__line--old",
  "effortsw",
  "empty",
  "experimental-intro--wall",
  "experimental-intro__group",
  "experimental-intro__head",
  "export-media-placeholder",
  "feedback-panel__time",
  "flex",
  "flex-wrap",
  "font-medium",
  "font-mono",
  "gap-1",
  "gap-1.5",
  "group-hover:text-foreground",
  "heartbeat-editor__status--new",
  "heartbeat-filter-menu__list",
  "heartbeat-flat",
  "history-load-error",
  "history-modal-backdrop",
  "history-modal__actions",
  "history-modal__summary",
  "history-search-results",
  "hljs",
  "hover:bg-surface-raised",
  "hover:text-foreground",
  "icon-btn",
  "items-center",
  "lab-pick-dialog__backdrop",
  "max-w-[160px]",
  "md-rich-link--local",
  "mem-fact__name",
  "mem-fact__text",
  "move-group-panel--new",
  "mr-1",
  "msg-meta__copy",
  "muted",
  "p-0.5",
  "pinned-files-shelf",
  "project-tree__action-slot--add",
  "project-tree__action-slot--collapse",
  "project-tree__action-slot--color-filter",
  "project-tree__action-slot--group",
  "project-tree__folder-action--menu",
  "project-tree__section--projects",
  "prompt-shelf--clear-context",
  "prov-card--edit",
  "provider-access-more",
  "provider-url-input",
  "px-3",
  "py-1.5",
  "rc-group",
  "rc-outcome__reasons",
  "reasonix-confirm-dialog__message-error",
  "recovery-lineage-backdrop",
  "recovery-wait-banner__code",
  "recovery-wait-banner__countdown",
  "recovery-wait-banner__progress",
  "remote-connection-error-dialog",
  "remote-file-view",
  "remote-files__view",
  "remote-import",
  "remote-import__list",
  "remote-ports",
  "remote-server",
  "remote-surface--ready",
  "remote-tree__item",
  "remote-wizard-backdrop",
  "remote-wizard__rail-label",
  "rewind__files-tooltip",
  "rounded-full",
  "sandbox-write-roots__intro",
  "select-none",
  "session-export-page",
  "session-takeover-dialog",
  "session-takeover-dialog__state",
  "set-input",
  "set-rules--readonly",
  "settings-actions",
  "settings-config-path--shadowed",
  "settings-error",
  "settings-field__value",
  "settings-load-error",
  "settings-page--localserver",
  "settings-restart-banner",
  "settings-restart-banner__action",
  "settings-section__header",
  "settings-toggle",
  "shortcuts-cheatsheet-backdrop",
  "slashmenu__item--search",
  "slashmenu__search",
  "stat--balance",
  "status-bar-items-editor__pane--visible",
  "statusbar__avg",
  "statusbar__cache-tokens",
  "statusbar__ctx",
  "statusbar__group--items",
  "statusbar__metric",
  "statusbar__metric--avg",
  "statusbar__metric--balance",
  "statusbar__metric--cache",
  "statusbar__metric--cache-tokens",
  "statusbar__metric--compact",
  "statusbar__metric--cost",
  "statusbar__metric--ctx",
  "statusbar__metric--output-tokens",
  "statusbar__metric--tokens",
  "statusbar__metric--tps",
  "statusbar__metric--turn-cost",
  "statusbar__metric--turn-tokens",
  "statusbar__metric--turns",
  "statusbar__metric--workspace",
  "statusbar__output-tokens",
  "statusbar__tokens",
  "statusbar__tps",
  "statusbar__turn-cost",
  "statusbar__turn-tokens",
  "statusbar__turns",
  "subagentpolicysw",
  "subagentpolicysw__menu",
  "subagents-effort-picker",
  "subagents-panel__icon",
  "subagents-panel__section",
  "taskmonitor__err",
  "taskmonitor__err-summary",
  "taskmonitor__event-list",
  "taskmonitor__event-seq",
  "taskmonitor__event-time",
  "taskmonitor__event-type",
  "taskmonitor__events-count",
  "taskmonitor__events-head",
  "taskmonitor__filters",
  "taskmonitor__indexing",
  "taskmonitor__load-more",
  "taskmonitor__project",
  "taskmonitor__spinner",
  "taskmonitor__state--empty",
  "taskmonitor__terminal",
  "text-[10px]",
  "text-[11px]",
  "text-accent",
  "text-muted",
  "text-muted-foreground",
  "text-red-500",
  "text-xs",
  "theme-gallery__detail-copy",
  "theme-gallery__open-preview",
  "tool__error-details",
  "tool__mcp-app",
  "tool__search-summary-text",
  "tool__subagent-preview-section",
  "transcript-question-jump-overlay",
  "transcript__window",
  "transition-all",
  "transition-colors",
  "truncate",
  "turn-result-summary__changes",
  "workspace-tree-menu",
  "workspace-tree__sizer",
]);
// Assertion 2: files still carrying Tailwind classes from before this contract
// existed (PinnedFilesShelf 型). Tier-2 cleanup removes the file from here.
const TAILWIND_EXEMPT_FILES = new Set([
// Legacy files carrying Tailwind-shaped classes before this contract existed
// (PinnedFilesShelf 型). Tier-2 cleanup removes entries; never add.
  "App.tsx",
  "app-shell/SidebarRegion.tsx",
  "components/Composer.tsx",
  "components/ModelImageInputControl.tsx",
  "components/PinnedFilesShelf.tsx",
  "components/ProjectTree.tsx",
  "components/SettingsPanel.tsx",
]);
// Assertion 3: selector|property pairs with deliberate hardcoded colors
// (group D awaits design ruling; --close red hover is Windows convention).
const HARDCOLOR_WHITELIST = new Set([
// Existing token-bypassing color declarations (group D of the 578 audit plus
// other deliberate fixed-color sites). Design rulings may shrink this; never add.
  "#crash-overlay { background: #1a1a2e }",
  "#crash-overlay { color: #e6e6f0 }",
  ".app--creation .composer-modebar__item--active, :root[data-theme-style] .app--creation .composer-modebar__item--active { color: #3b82f6 }",
  ".app--creation .composer-modebar__item--auto.composer-modebar__item--active, :root[data-theme-style] .app--creation .composer-modebar__item--auto.composer-modebar__item--active { color: #22c55e }",
  ".app--creation .composer-modebar__item--yolo.composer-modebar__item--active, :root[data-theme-style] .app--creation .composer-modebar__item--yolo.composer-modebar__item--active { color: #f97316 }",
  ".app--creation .composer__btn--send:not(:disabled), :root[data-theme-style] .app--creation .composer__btn--send:not(:disabled) { color: #fff }",
  ".app--creation .set-seg__btn--on, :root[data-theme-style] .app--creation .set-seg__btn--on { box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06) }",
  ".app--creation .settings-modal, :root[data-theme-style] .app--creation .settings-modal { box-shadow: 0 24px 72px rgba(0, 0, 0, 0.18), 0 2px 8px rgba(0, 0, 0, 0.06) }",
  ".app--creation .settings-modal-backdrop, :root[data-theme-style] .app--creation .settings-modal-backdrop { background: rgba(18, 20, 26, 0.36) }",
  ".app--creation .settings-section, .app--creation .settings-page--manager .mem-section, :root[data-theme-style] .app--creation .settings-section, :root[data-theme-style] .app--creation .settings-page--manager .mem-section { box-shadow: 0 1px 2px rgba(0, 0, 0, 0.04) }",
  ".app--creation .sidebar-collapse-toggle, :root[data-theme-style] .app--creation .sidebar-collapse-toggle { box-shadow: 0 8px 20px color-mix(in srgb, #000 14%, transparent) }",
  ".app--creation .turn-actions__menu, :root[data-theme-style] .app--creation .turn-actions__menu { box-shadow: 0 12px 34px rgba(0, 0, 0, 0.28) }",
  ".badge--feedback { border-color: rgba(217, 164, 65, 0.4) }",
  ".badge--local { border-color: rgba(217, 164, 65, 0.4) }",
  ".badge--project { border-color: rgba(116, 184, 122, 0.4) }",
  ".bot-connect-panel__qr img, .bot-connect-panel__qr-code { background: #fff }",
  ".bot-detail__avatar { color: #fff }",
  ".bot-detail__avatar--feishu { background: #2f80ff }",
  ".bot-detail__avatar--lark { background: #4f7dff }",
  ".bot-detail__avatar--qq { background: #ff5a3d }",
  ".bot-detail__avatar--weixin { background: #22b35f }",
  ".bot-mobile-remote__qr-code { background: #ffffff }",
  ".cap-tab--active { box-shadow: 0 1px 3px rgba(0, 0, 0, 0.08) }",
  ".code-line-row--current { background: rgba(255, 193, 7, 0.22) }",
  ".code-line-row--current { outline: 1px solid rgba(255, 193, 7, 0.35) }",
  ".code-search { box-shadow: 0 4px 16px rgba(0, 0, 0, 0.15) }",
  ".code-search-hl { background: rgba(255, 193, 7, 0.45) }",
  ".code-search-hl--current { background: rgba(255, 193, 7, 0.72) }",
  ".collab-card__status--blocked, .collab-card__status--failed { color: #d9534f }",
  ".collab-card__status--done { color: #2e9e5b }",
  ".collab-inbox-panel { box-shadow: 0 8px 28px rgba(0, 0, 0, 0.28) }",
  ".composer-access-menu { box-shadow: 0 14px 34px rgba(0, 0, 0, 0.28) }",
  ".composer-card { box-shadow: 0 10px 28px rgba(0, 0, 0, 0.08) }",
  ".composer-content-menu__back { border-bottom: 1px solid rgba(127, 127, 127, 0.22) }",
  ".composer-content-menu__back:hover { background: rgba(127, 127, 127, 0.1) }",
  ".composer-context__remove, .composer-context__item > .tooltip-trigger:last-child button { background: color-mix(in srgb, #000 78%, transparent) }",
  ".composer-context__remove, .composer-context__item > .tooltip-trigger:last-child button { color: #fff }",
  ".composer-context__remove:hover, .composer-context__item > .tooltip-trigger:last-child button:hover { background: color-mix(in srgb, #000 88%, transparent) }",
  ".composer-context__remove:hover, .composer-context__item > .tooltip-trigger:last-child button:hover { color: #fff }",
  ".composer-guidance-item { box-shadow: inset 0 1px 0 color-mix(in srgb, #fff 5%, transparent) }",
  ".composer-modebar__thumb { box-shadow: inset 0 1px 0 color-mix(in srgb, #fff 18%, transparent), 0 1px 3px color-mix(in srgb, #000 28%, transparent) }",
  ".context-menu { box-shadow: 0 16px 36px rgba(0, 0, 0, 0.36) }",
  ".context-menu__item--danger:not(:disabled):hover, .context-menu__item--danger:not(:disabled):focus-visible { color: #e5534b !important }",
  ".context-panel__legend-dot--completion { background: #2f6df6 }",
  ".context-panel__legend-dot--prompt { background: #13a7a5 }",
  ".context-panel__legend-dot--reasoning { background: #f97316 }",
  ".context-panel__source-bar-segment--hit { background: #16a34a }",
  ".context-panel__source-bar-segment--input { background: #13a7a5 }",
  ".context-panel__source-bar-segment--miss { background: #f97316 }",
  ".context-panel__source-bar-segment--output { background: #2f6df6 }",
  ".context-panel__source-tone--amber { background: #f59e0b }",
  ".context-panel__source-tone--blue { background: #2f6df6 }",
  ".context-panel__source-tone--rose { background: #e11d48 }",
  ".context-panel__source-tone--slate { background: #64748b }",
  ".context-panel__source-tone--teal { background: #13a7a5 }",
  ".context-panel__source-tone--violet { background: #7c3aed }",
  ".context-panel__type-share--completion, .context-panel__type-dot--completion { background: #7a8fb8 }",
  ".context-panel__type-share--other, .context-panel__type-dot--other { background: #c7c7c7 }",
  ".context-panel__type-share--prompt, .context-panel__type-dot--prompt { background: #6b8f8f }",
  ".context-panel__type-share--reasoning, .context-panel__type-dot--reasoning { background: #b48a58 }",
  ".crash-overlay__copy, .crash-overlay__send { background: #2a2a40 }",
  ".crash-overlay__copy, .crash-overlay__send { border: 1px solid #444 }",
  ".crash-overlay__note { color: #8a8aa3 }",
  ".crash-overlay__title { color: #ff6b6b }",
  ".drawer { box-shadow: -18px 0 48px rgba(0, 0, 0, 0.46) }",
  ".drawer-backdrop { background: rgba(0, 0, 0, 0.4) }",
  ".drawer-backdrop--subtle { background: rgba(0, 0, 0, 0.16) }",
  ".drawer-backdrop:has(> .palette) { background: rgba(0, 0, 0, 0.42) }",
  ".external-opener__app-icon--editor .external-opener__fallback-icon { background: linear-gradient(145deg, #7867f2, #3b54c9) }",
  ".external-opener__app-icon--file-manager .external-opener__fallback-icon { background: linear-gradient(145deg, #39bdf8, #1677e8) }",
  ".external-opener__app-icon--terminal .external-opener__fallback-icon { background: linear-gradient(145deg, #41454e, #15171b) }",
  ".external-opener__fallback-icon { box-shadow: inset 0 0 0 1px color-mix(in srgb, #fff 18%, transparent), 0 1px 2px color-mix(in srgb, #000 18%, transparent) }",
  ".external-opener__fallback-icon { color: #fff }",
  ".external-opener__menu { box-shadow: 0 18px 42px color-mix(in srgb, #000 32%, transparent) }",
  ".feedback-panel { box-shadow: 0 8px 28px rgba(0, 0, 0, 0.28) }",
  ".floating-menu { box-shadow: 0 12px 30px rgba(0, 0, 0, 0.36) }",
  ".fork-features-dialog { box-shadow: 0 18px 60px rgba(0, 0, 0, 0.45) }",
  ".guidance-hover-preview { box-shadow: 0 10px 28px rgba(0, 0, 0, 0.28) }",
  ".heartbeat-btn--primary { color: #fff }",
  ".heartbeat-editor__action-btn--primary { color: #fff }",
  ".heartbeat-editor__toggle--on { background: #4caf50 }",
  ".heartbeat-editor__toggle--on:hover { background: #43a047 }",
  ".heartbeat-editor__toggle-knob { background: #fff }",
  ".heartbeat-editor__toggle-knob { box-shadow: 0 1px 3px rgba(0, 0, 0, 0.25) }",
  ".heartbeat-editor__weekday-btn--on { color: #fff }",
  ".heartbeat-empty--guided .heartbeat-btn--primary { color: #fff }",
  ".heartbeat-filter-menu { box-shadow: 0 4px 12px rgba(0,0,0,0.15) }",
  ".heartbeat-project-menu { box-shadow: 0 6px 20px rgba(0, 0, 0, 0.2) }",
  ".heartbeat-project-menu__item--active { background: rgba(128, 128, 128, 0.08) }",
  ".heartbeat-task-menu { box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15) }",
  ".heartbeat-toolbar__btn--primary, .heartbeat-toolbar__btn--primary:hover { color: #fff }",
  ".hist-item__badge--open::before { background: #3b82f6 }",
  ".history-clear--confirm { background: rgba(255, 139, 109, 0.1) }",
  ".history-clear--confirm { border-color: rgba(255, 139, 109, 0.45) }",
  ".history-clear--confirm { color: #ff8b6d }",
  ".history-clear--confirm:hover { background: rgba(255, 139, 109, 0.16) }",
  ".history-clear--confirm:hover { border-color: rgba(255, 139, 109, 0.62) }",
  ".history-clear--confirm:hover { color: #ff9f86 }",
  ".image-viewer__image { box-shadow: 0 4px 24px rgba(0, 0, 0, 0.18) }",
  ".jump-preview { box-shadow: 0 8px 24px rgba(0, 0, 0, 0.18) }",
  ".lab-pick-dialog { box-shadow: 0 18px 60px rgba(0, 0, 0, 0.45) }",
  ".layout--workspace-overlay .workbench-dock--overlay { box-shadow: -12px 0 36px rgba(0, 0, 0, 0.24) }",
  ".management-modal { box-shadow: 0 16px 48px rgba(0, 0, 0, 0.32) }",
  ".management-modal-backdrop { background: rgba(0, 0, 0, 0.48) }",
  ".mcp-app-frame { background: #fff }",
  ".mem-doc:hover { box-shadow: 0 4px 14px -8px rgba(0, 0, 0, 0.5) }",
  ".mem-fact:hover { box-shadow: 0 4px 14px -8px rgba(0, 0, 0, 0.5) }",
  ".mem-ws-select__menu { box-shadow: 0 8px 24px rgba(0, 0, 0, 0.24) }",
  ".menu { box-shadow: 0 10px 30px rgba(0, 0, 0, 0.35) }",
  ".mermaid-diagram__preview { background: #ffffff }",
  ".modal { box-shadow: 0 16px 48px rgba(0, 0, 0, 0.45) }",
  ".modal-backdrop { background: rgba(0, 0, 0, 0.55) }",
  ".model-setting-help-popover { box-shadow: 0 8px 28px #0002 }",
  ".modelsw__menu { box-shadow: 0 12px 32px rgba(0, 0, 0, 0.45) }",
  ".move-group-panel { box-shadow: 0 12px 32px rgba(0, 0, 0, 0.22) }",
  ".move-group__swatch { border: 1px solid rgba(0, 0, 0, 0.2) }",
  ".onboarding { background: radial-gradient( ellipse at top, rgba(217, 119, 87, 0.06) 0%, transparent 60% ), rgba(7, 8, 10, 0.92) }",
  ".onboarding__card { box-shadow: 0 24px 60px rgba(0, 0, 0, 0.55), 0 0 0 1px rgba(217, 119, 87, 0.04) }",
  ".onboarding__error { background: rgba(255, 70, 70, 0.08) }",
  ".onboarding__error { border: 1px solid rgba(255, 70, 70, 0.25) }",
  ".onboarding__error { color: #ff8b7a }",
  ".onboarding__input:focus { box-shadow: 0 0 0 3px rgba(217, 119, 87, 0.18) }",
  ".onboarding__link:hover { color: #e08868 }",
  ".onboarding__spinner { border-top-color: #1a0e08 }",
  ".onboarding__spinner { border: 2px solid rgba(26, 14, 8, 0.3) }",
  ".onboarding__submit { color: #1a0e08 }",
  ".onboarding__submit:hover:not(:disabled) { background: #e08868 }",
  ".palette { box-shadow: 0 24px 70px rgba(0, 0, 0, 0.5) }",
  ".project-tree__group-main--reorder-target { background: rgba(127, 127, 127, 0.16) }",
  ".project-tree__group-main--reorder-target { box-shadow: inset 0 0 0 1px rgba(127, 127, 127, 0.5) }",
  ".project-tree__hover-card { box-shadow: 0 6px 20px rgba(0, 0, 0, 0.22) }",
  ".project-tree__time-filter-menu { box-shadow: 0 4px 16px rgba(0, 0, 0, 0.18) }",
  ".project-tree__topic-im { background: color-mix(in srgb, #3b82f6 10%, transparent) }",
  ".project-tree__topic-im--feishu, .project-tree__topic-im--lark { background: color-mix(in srgb, #0ea5e9 11%, transparent) }",
  ".project-tree__topic-im--weixin { background: color-mix(in srgb, #22c55e 11%, transparent) }",
  ".prompt-action--danger.prompt-action--selected .prompt-action__key { color: #fff }",
  ".prompt-shelf--compact .prompt-shelf__card { box-shadow: 0 5px 14px rgba(0, 0, 0, 0.06) }",
  ".prompt-shelf__card { background: rgba(64, 64, 64, 0.06) }",
  ".prov-card { box-shadow: 0 8px 20px rgba(0, 0, 0, 0.08) }",
  ".provider-access-more__menu { box-shadow: 0 18px 42px rgba(0, 0, 0, 0.42) }",
  ".provider-model-dialog::backdrop { background: #0008 }",
  ".question-search-panel { box-shadow: 0 12px 32px rgba(0, 0, 0, 0.22) }",
  ".rc-preview__msg { background: rgba(127, 127, 127, 0.05) }",
  ".rc-preview__section { background: rgba(127, 127, 127, 0.06) }",
  ".remote-hostkey-overlay { background: rgba(0, 0, 0, 0.5) }",
  ".remote-switcher { box-shadow: 0 16px 44px rgba(0, 0, 0, 0.48) }",
  ".remote-wizard__rail-item--done .remote-wizard__rail-index { color: #fff }",
  ".rewind__btn { box-shadow: 0 1px 4px rgba(0,0,0,0.12) }",
  ".rewind__menu { box-shadow: 0 6px 20px rgba(0, 0, 0, 0.28) }",
  ".session-monitor { box-shadow: 0 12px 32px rgba(0, 0, 0, 0.32) }",
  ".session-wall { box-shadow: 0 24px 70px rgba(0, 0, 0, 0.5) }",
  ".session-wall-backdrop:has(> .session-wall) { background: rgba(0, 0, 0, 0.42) }",
  ".settings-section, .settings-page--manager .mem-section { box-shadow: 0 10px 24px rgba(0, 0, 0, 0.08) }",
  ".settings-select-menu { box-shadow: 0 6px 20px rgb(0 0 0 / 14%) }",
  ".sidebar-im-row__platform { color: #fff }",
  ".sidebar-im-row__platform--feishu { background: #2f80ff }",
  ".sidebar-im-row__platform--lark { background: #4f7dff }",
  ".sidebar-im-row__platform--qq { background: #ff5a3d }",
  ".sidebar-im-row__platform--weixin { background: #22b35f }",
  ".skip-to-composer:focus-visible { box-shadow: 0 12px 28px rgba(0, 0, 0, 0.24) }",
  ".slashmenu { box-shadow: 0 12px 32px rgba(0, 0, 0, 0.4) }",
  ".slider-thumb { background: #fff }",
  ".taskmonitor-popover { box-shadow: 0 14px 36px rgba(0,0,0,.42) }",
  ".taskmonitor__confirm .taskmonitor__confirm-stop { color: #fff }",
  ".taskmonitor__count { color: #fff }",
  ".taskmonitor__error { color: #ef4444 }",
  ".taskmonitor__message { color: #4ade80 }",
  ".taskmonitor__state--error { color: #ef4444 }",
  ".theme-card__swatches[data-theme-style-card=\"amber\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #d4632f, #de7a4b) }",
  ".theme-card__swatches[data-theme-style-card=\"amber\"] .theme-card__swatch--bg { background: #0f0c08 }",
  ".theme-card__swatches[data-theme-style-card=\"amber\"] .theme-card__swatch--surface { background: #201a12 }",
  ".theme-card__swatches[data-theme-style-card=\"aurora\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #8b7cff, #b07cff 42%, #38d6e6) }",
  ".theme-card__swatches[data-theme-style-card=\"aurora\"] .theme-card__swatch--bg { background: #0e0d18 }",
  ".theme-card__swatches[data-theme-style-card=\"aurora\"] .theme-card__swatch--surface { background: #1d1b34 }",
  ".theme-card__swatches[data-theme-style-card=\"carbon\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #2dd4bf, #22d3ee) }",
  ".theme-card__swatches[data-theme-style-card=\"carbon\"] .theme-card__swatch--bg { background: #0e0d0c }",
  ".theme-card__swatches[data-theme-style-card=\"carbon\"] .theme-card__swatch--surface { background: #1e1c1a }",
  ".theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #ff6a3d, #ff9a52) }",
  ".theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--bg { background: #0c0d10 }",
  ".theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--surface { background: #15161a }",
  ".theme-card__swatches[data-theme-style-card=\"nocturne\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #818cf8, #a78bfa) }",
  ".theme-card__swatches[data-theme-style-card=\"nocturne\"] .theme-card__swatch--bg { background: #101019 }",
  ".theme-card__swatches[data-theme-style-card=\"nocturne\"] .theme-card__swatch--surface { background: #20212f }",
  ".theme-card__swatches[data-theme-style-card=\"slate\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #4d8df6, #3b82f6) }",
  ".theme-card__swatches[data-theme-style-card=\"slate\"] .theme-card__swatch--bg { background: #0d0f12 }",
  ".theme-card__swatches[data-theme-style-card=\"slate\"] .theme-card__swatch--surface { background: #1b1f25 }",
  ".theme-editor__focus { border: 2px solid #fff }",
  ".theme-editor__focus { box-shadow: 0 0 0 1px rgba(0, 0, 0, 0.55) }",
  ".theme-gallery__editor { box-shadow: 0 24px 72px color-mix(in srgb, #000 42%, transparent) }",
  ".theme-gallery__editor-overlay { background: color-mix(in srgb, #000 58%, transparent) }",
  ".theme-gallery__menu { box-shadow: 0 8px 24px color-mix(in srgb, #000 28%, transparent) }",
  ".theme-reset-btn { background: #f3f4f6 !important }",
  ".theme-reset-btn { border: 1px solid #d1d5db !important }",
  ".theme-reset-btn { color: #111827 !important }",
  ".toast { box-shadow: 0 4px 24px rgba(0, 0, 0, 0.2) }",
  ".tooltip { box-shadow: 0 10px 28px rgba(0, 0, 0, 0.28) }",
  ".topicbar__export-menu { box-shadow: 0 14px 34px rgba(0, 0, 0, 0.34) }",
  ".topicbar__export-menu { box-shadow: 0 16px 36px rgba(0, 0, 0, 0.36) }",
  ".topicbar__more-export-menu { box-shadow: 0 16px 36px rgba(0, 0, 0, 0.36) }",
  ".topicbar__more-menu { box-shadow: 0 16px 36px rgba(0, 0, 0, 0.36) }",
  ".topicbar__source-chip { background: color-mix(in srgb, #0ea5e9 8%, transparent) }",
  ".topicbar__source-chip--weixin { background: color-mix(in srgb, #22c55e 8%, transparent) }",
  ".transcript-find { box-shadow: 0 12px 32px rgba(0, 0, 0, 0.22) }",
  ".transcript-selection-action { box-shadow: 0 12px 30px rgba(0, 0, 0, 0.28) }",
  ".transcript-selection-result-card { box-shadow: 0 12px 30px rgba(0, 0, 0, 0.28) }",
  ".usage-stats__tip { box-shadow: 0 6px 20px rgba(0, 0, 0, 0.18) }",
  ".windows-window-control--close:hover, .windows-window-control--close:focus-visible { background: #c42b1c }",
  ".windows-window-control--close:hover, .windows-window-control--close:focus-visible { color: #fff }",
  ".workbench-dock__tab--active { box-shadow: 0 1px 2px rgba(0, 0, 0, 0.16) }",
  ".workspace-branch-menu { box-shadow: 0 6px 20px rgba(0, 0, 0, 0.2) }",
  ".workspace-files__tab--active { box-shadow: 0 1px 2px rgba(0, 0, 0, 0.16) }",
  ".workspace-recent-menu { box-shadow: 0 14px 34px rgba(0, 0, 0, 0.28) }",
  ".worktree-node--task.worktree-node--selected .worktree-node__dot--on { background: #4caf50 }",
  ".worktree-node__dot--on { background: #4caf50 }",
  ":root .composer-menu-surface, :root[data-theme-style] .composer-menu-surface { box-shadow: 0 14px 34px rgba(0, 0, 0, 0.28) }",
  ":root:not([data-theme=\"light\"]) .mermaid-diagram__preview { background: #111319 }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"amber\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #dd5b28, #ef8a4a) }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"amber\"] .theme-card__swatch--bg { background: #f5f6f9 }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"amber\"] .theme-card__swatch--surface { background: #ffffff }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"aurora\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #6d5efc, #9b5cf6 42%, #21c8d8) }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"aurora\"] .theme-card__swatch--bg { background: #f6f3fb }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"aurora\"] .theme-card__swatch--surface { background: #fdfcff }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"carbon\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #0d9488, #0891b2) }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"carbon\"] .theme-card__swatch--bg { background: #f6f4f0 }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"carbon\"] .theme-card__swatch--surface { background: #ffffff }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #ff5a2c, #ff8a3d) }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--bg { background: #f4f3ef }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--surface { background: #ffffff }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"nocturne\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #6366f1, #8b5cf6) }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"nocturne\"] .theme-card__swatch--bg { background: #f6f5fb }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"nocturne\"] .theme-card__swatch--surface { background: #fdfcff }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"slate\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #2f6fe0, #3b82f6) }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"slate\"] .theme-card__swatch--bg { background: #f5f6f9 }",
  ":root:not([data-theme]) .theme-card__swatches[data-theme-style-card=\"slate\"] .theme-card__swatch--surface { background: #ffffff }",
  ":root:not([data-theme]) .theme-reset-btn { background: #f3f4f6 !important }",
  ":root:not([data-theme]) .theme-reset-btn { border: 1px solid #d1d5db !important }",
  ":root:not([data-theme]) .theme-reset-btn { color: #111827 !important }",
  ":root:not([data-theme]) .topicbar { background: #f5f6f9 !important }",
  ":root[data-theme-style=\"graphite\"] .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #ff5a2c, #ff8a3d) }",
  ":root[data-theme-style=\"graphite\"] .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--bg { background: #f4f3ef }",
  ":root[data-theme-style=\"graphite\"] .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--surface { background: #ffffff }",
  ":root[data-theme-style] .app--creation .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--accent { background: linear-gradient(128deg, #604116 0%, #9b7226 28%, #d8ad4f 48%, #f3d77b 57%, #b9872d 78%, #704d19 100%) }",
  ":root[data-theme-style] .app--creation .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--bg { background: #0c0d10 }",
  ":root[data-theme-style] .app--creation .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--surface { background: #15161a }",
  ":root[data-theme-style] .app-chrome__traffic span { box-shadow: inset 0 0 0 0.5px rgba(0, 0, 0, 0.08) }",
  ":root[data-theme-style] .app-chrome__traffic span:nth-child(1) { background: #ff5f57 }",
  ":root[data-theme-style] .app-chrome__traffic span:nth-child(2) { background: #febc2e }",
  ":root[data-theme-style] .app-chrome__traffic span:nth-child(3) { background: #28c840 }",
  ":root[data-theme-style] .composer-modebar__thumb { box-shadow: inset 0 1px 0 color-mix(in srgb, #fff 18%, transparent), 0 1px 3px color-mix(in srgb, #000 28%, transparent) }",
  ":root[data-theme-style] .drawer-backdrop:has(> .palette) { background: rgba(20, 22, 28, 0.26) }",
  ":root[data-theme-style] .management-modal-backdrop, :root[data-theme-style] .settings-modal-backdrop { background: rgba(20, 22, 28, 0.26) }",
  ":root[data-theme-style] .sidebar { box-shadow: 10px 0 30px rgba(20, 22, 28, 0.1) }",
  ":root[data-theme-style] .tabbar__command-kbd { box-shadow: inset 0 1px 0 color-mix(in srgb, #fff 58%, transparent) }",
  ":root[data-theme-style]:not([data-theme]) .app--creation .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--accent { background: linear-gradient(128deg, #604116 0%, #9b7226 28%, #d8ad4f 48%, #f3d77b 57%, #b9872d 78%, #704d19 100%) }",
  ":root[data-theme-style]:not([data-theme]) .app--creation .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--bg { background: #f4f3ef }",
  ":root[data-theme-style]:not([data-theme]) .app--creation .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--surface { background: #ffffff }",
  ":root[data-theme=\"dark\"] .mermaid-diagram__preview { background: #111319 }",
  ":root[data-theme=\"dark\"] .prompt-shelf__card, :root[data-theme=\"dark\"][data-theme-style] .prompt-shelf__card { background: rgba(255, 255, 255, 0.08) }",
  ":root[data-theme=\"dark\"] .theme-reset-btn, :root:not([data-theme]) .theme-reset-btn { background: #1f2937 !important }",
  ":root[data-theme=\"dark\"] .theme-reset-btn, :root:not([data-theme]) .theme-reset-btn { border-color: #4b5563 !important }",
  ":root[data-theme=\"dark\"] .theme-reset-btn, :root:not([data-theme]) .theme-reset-btn { color: #f9fafb !important }",
  ":root[data-theme=\"light\"] .prompt-shelf__card, :root[data-theme=\"light\"][data-theme-style] .prompt-shelf__card { background: rgba(64, 64, 64, 0.05) }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"amber\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #dd5b28, #ef8a4a) }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"amber\"] .theme-card__swatch--bg { background: #f5f6f9 }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"amber\"] .theme-card__swatch--surface { background: #ffffff }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"aurora\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #6d5efc, #9b5cf6 42%, #21c8d8) }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"aurora\"] .theme-card__swatch--bg { background: #f6f3fb }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"aurora\"] .theme-card__swatch--surface { background: #fdfcff }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"carbon\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #0d9488, #0891b2) }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"carbon\"] .theme-card__swatch--bg { background: #f6f4f0 }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"carbon\"] .theme-card__swatch--surface { background: #ffffff }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #ff5a2c, #ff8a3d) }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--bg { background: #f4f3ef }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--surface { background: #ffffff }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"nocturne\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #6366f1, #8b5cf6) }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"nocturne\"] .theme-card__swatch--bg { background: #f6f5fb }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"nocturne\"] .theme-card__swatch--surface { background: #fdfcff }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"slate\"] .theme-card__swatch--accent { background: linear-gradient(120deg, #2f6fe0, #3b82f6) }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"slate\"] .theme-card__swatch--bg { background: #f5f6f9 }",
  ":root[data-theme=\"light\"] .theme-card__swatches[data-theme-style-card=\"slate\"] .theme-card__swatch--surface { background: #ffffff }",
  ":root[data-theme=\"light\"][data-theme-style] .app--creation .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--accent { background: linear-gradient(128deg, #604116 0%, #9b7226 28%, #d8ad4f 48%, #f3d77b 57%, #b9872d 78%, #704d19 100%) }",
  ":root[data-theme=\"light\"][data-theme-style] .app--creation .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--bg { background: #f4f3ef }",
  ":root[data-theme=\"light\"][data-theme-style] .app--creation .theme-card__swatches[data-theme-style-card=\"graphite\"] .theme-card__swatch--surface { background: #ffffff }",
  ":root[data-theme=\"light\"][data-theme-style] .topicbar, :root[data-theme=\"light\"] .topicbar { background: #f5f6f9 !important }",
]);

// ── Scanning primitives ────────────────────────────────────────────────────
function stripCssComments(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, " ");
}

// Extract (selector, body) pairs from CSS text, handling @media nesting by
// matching inner rules individually (outer preludes carry no declarations).
function extractCssRules(cssText) {
  const rules = [];
  const pattern = /([^{}]+)\{([^{}]*)\}/g;
  let match;
  while ((match = pattern.exec(cssText)) !== null) {
    const selector = match[1].replace(/\s+/g, " ").trim();
    const body = match[2];
    if (selector) rules.push({ selector, body });
  }
  return rules;
}

function classNamesInSelector(selector) {
  const names = [];
  const pattern = /\.([a-zA-Z_][a-zA-Z0-9_-]*)/g;
  let match;
  while ((match = pattern.exec(selector)) !== null) names.push(match[1]);
  return names;
}

// CSS-defined class set: every *.css under src plus TSX inline <style> blocks
// and template-literal style constants (e.g. sessionExport's EXPORT_STYLES).
function collectDefinedClasses(files) {
  const defined = new Set();
  for (const file of files.css) {
    const rules = extractCssRules(stripCssComments(fs.readFileSync(file, "utf8")));
    for (const rule of rules) for (const name of classNamesInSelector(rule.selector)) defined.add(name);
  }
  for (const file of files.ts) {
    const text = fs.readFileSync(file, "utf8");
    for (const styleBlock of text.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)) {
      const css = styleBlock[1].replace(/\$\{[\s\S]*?\}/g, " ");
      for (const rule of extractCssRules(stripCssComments(css))) {
        for (const name of classNamesInSelector(rule.selector)) defined.add(name);
      }
    }
    for (const constant of text.matchAll(/const\s+\w*[Ss]tyles?\w*\s*=\s*`([^`]*)`/g)) {
      const css = constant[1].replace(/\$\{[\s\S]*?\}/g, " ");
      for (const rule of extractCssRules(stripCssComments(css))) {
        for (const name of classNamesInSelector(rule.selector)) defined.add(name);
      }
    }
  }
  return defined;
}

// className string literals only. Template-literal and expression classNames
// are deliberately invisible here (assertion 4 exemption: dynamic classes).
function collectReferences(files) {
  const refs = new Map(); // class -> [file:line]
  const patterns = [/className=\{?"([^"]+)"/g, /className=\{'([^']+)'/g];
  const addRef = (name, where) => {
    if (!refs.has(name)) refs.set(name, []);
    refs.get(name).push(where);
  };
  for (const file of files.ts) {
    const lines = fs.readFileSync(file, "utf8").split("\n");
    for (let i = 0; i < lines.length; i++) {
      for (const pattern of patterns) {
        pattern.lastIndex = 0;
        let match;
        while ((match = pattern.exec(lines[i])) !== null) {
          for (const name of match[1].trim().split(/\s+/)) {
            if (name) addRef(name, `${path.relative(frontendRoot, file)}:${i + 1}`);
          }
        }
      }
    }
  }
  return refs;
}

const TAILWIND_VARIANT_PREFIX = /^(?:hover|focus|focus-visible|focus-within|active|visited|disabled|group-hover|peer|dark|first|last|odd|even|before|after|sm|md|lg|xl|2xl|3xl):/;
const TAILWIND_ARBITRARY = /^[a-z-]+-\[[^\]]+\]$/;
const TAILWIND_TOKENS = new Set([
  "flex", "flex-row", "flex-col", "flex-wrap", "flex-1", "flex-none", "flex-shrink-0", "flex-grow",
  "grid", "inline-flex", "inline-block", "inline", "block", "hidden", "contents",
  "items-center", "items-start", "items-end", "items-baseline", "items-stretch",
  "justify-center", "justify-start", "justify-end", "justify-between", "justify-around", "justify-evenly",
  "self-center", "self-start", "self-end", "self-stretch", "grow", "grow-0", "shrink", "shrink-0",
  "relative", "absolute", "fixed", "sticky", "inset-0", "isolate", "z-0", "z-10", "z-20", "z-50",
  "rounded-sm", "rounded-md", "rounded-lg", "rounded-xl", "rounded-2xl", "rounded-3xl", "rounded-full",
  "rounded-t", "rounded-b", "rounded-l", "rounded-r", "rounded-tl", "rounded-tr", "rounded-bl", "rounded-br",
  "border-0", "border-2", "border-4", "border-t", "border-b", "border-l", "border-r", "border-solid", "border-dashed",
  "shadow-sm", "shadow", "shadow-md", "shadow-lg", "shadow-xl", "shadow-2xl", "shadow-none",
  "ring-0", "ring-1", "ring-2", "ring-4",
  "truncate", "uppercase", "lowercase", "capitalize", "italic", "underline", "line-through", "no-underline",
  "antialiased", "appearance-none", "outline-none", "sr-only", "not-sr-only",
  "overflow-hidden", "overflow-auto", "overflow-visible", "overflow-scroll", "overflow-ellipsis",
  "whitespace-nowrap", "whitespace-pre", "whitespace-pre-wrap", "break-words", "break-all",
  "object-cover", "object-contain", "object-fill",
  "cursor-pointer", "cursor-default", "cursor-not-allowed", "select-none", "select-text", "select-all",
  "pointer-events-none", "pointer-events-auto",
  "transition", "transition-all", "transition-colors", "transition-opacity", "transition-transform", "transition-none",
  "duration-150", "duration-200", "duration-300", "ease-in", "ease-out", "ease-in-out",
  "opacity-0", "opacity-25", "opacity-50", "opacity-75", "opacity-100",
  "visible", "invisible", "collapse", "resize-none", "resize-y", "align-middle", "align-top", "align-bottom",
  "leading-none", "leading-tight", "leading-snug", "leading-normal", "leading-relaxed", "leading-loose",
  "font-normal", "font-medium", "font-semibold", "font-bold", "font-extrabold", "font-light",
  "text-xs", "text-sm", "text-base", "text-lg", "text-xl", "text-2xl", "text-3xl", "text-4xl",
  "text-left", "text-center", "text-right", "text-justify",
  "w-full", "w-screen", "w-fit", "h-full", "h-screen", "h-fit", "min-w-0", "min-h-0", "max-w-full", "max-h-full",
  "mx-auto", "my-auto", "ml-auto", "mr-auto", "mt-auto", "mb-auto", "px-1", "px-2", "px-3", "px-4",
  "backdrop-blur", "blur", "grayscale", "invert", "space-x-1", "space-x-2", "space-y-1", "space-y-2",
  "aspect-square", "aspect-video", "columns-1", "text-ellipsis", "overscroll-none", "overscroll-contain",
]);
function tailwindHits(token) {
  return TAILWIND_VARIANT_PREFIX.test(token) || TAILWIND_ARBITRARY.test(token) || TAILWIND_TOKENS.has(token);
}

const COLOR_PROP = /^(?:[-a-z]+-)?color$|^(?:background|border|outline|text-decoration)(?:-(?:top|right|bottom|left))?(?:-(?:color|image))?$|^box-shadow$|^fill$|^stroke$|^caret-color$|^accent-color$/;
const HARDCOLOR_VALUE = /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/;
function hardcodedColors(rules, fileLabel) {
  const hits = [];
  for (const rule of rules) {
    for (const decl of rule.body.split(";")) {
      const idx = decl.indexOf(":");
      if (idx < 0) continue;
      const prop = decl.slice(0, idx).trim();
      const value = decl.slice(idx + 1).trim();
      if (prop.startsWith("--")) continue; // token definitions are the one legitimate place
      if (!COLOR_PROP.test(prop)) continue;
      if (!HARDCOLOR_VALUE.test(value)) continue;
      if (value.includes("var(")) continue; // mixed with theme tokens = theme-aware (report口径)
      hits.push(`${fileLabel} ${rule.selector} { ${prop}: ${value} }`);
    }
  }
  return hits;
}

function listSourceFiles(root) {
  const css = [];
  const ts = [];
  const walk = (dir) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name === "__tests__") continue; // assertion 4: test hooks exempt
        walk(full);
      } else if (entry.isFile()) {
        if (entry.name.endsWith(".css")) css.push(full);
        else if (entry.name.endsWith(".tsx") || entry.name.endsWith(".ts")) {
          if (/\.(?:test|spec)\.[jt]sx?$/.test(entry.name)) continue;
          ts.push(full);
        }
      }
    }
  };
  walk(root);
  return { css, ts };
}

// ── One scan pass over a source root ───────────────────────────────────────
function scan(root) {
  const files = listSourceFiles(root);
  const defined = collectDefinedClasses(files);
  const refs = collectReferences(files);

  const missing = [];
  for (const [name, where] of refs) {
    if (!defined.has(name) && !BASELINE_WHITELIST.has(name)) missing.push({ name, where });
  }

  const tailwind = [];
  for (const file of files.ts) {
    const rel = path.relative(root, file).replace(/\\/g, "/");
    if (TAILWIND_EXEMPT_FILES.has(rel) || TAILWIND_EXEMPT_FILES.has(path.relative(frontendRoot, file).replace(/\\/g, "/"))) continue;
    const lines = fs.readFileSync(file, "utf8").split("\n");
    for (let i = 0; i < lines.length; i++) {
      for (const pattern of [/className=\{?"([^"]+)"/g, /className=\{'([^']+)'/g]) {
        pattern.lastIndex = 0;
        let match;
        while ((match = pattern.exec(lines[i])) !== null) {
          const hits = match[1].trim().split(/\s+/).filter(tailwindHits);
          if (hits.length) tailwind.push(`${rel}:${i + 1}  ${hits.join(" ")}`);
        }
      }
    }
  }

  const hardcolor = [];
  for (const file of files.css) {
    const rules = extractCssRules(stripCssComments(fs.readFileSync(file, "utf8")));
    for (const hit of hardcodedColors(rules, path.relative(root, file).replace(/\\/g, "/"))) {
      const key = hit.replace(/^[^ ]+ /, "").replace(/\s+/g, " ");
      if (HARDCOLOR_WHITELIST.has(key)) continue;
      hardcolor.push(hit);
    }
  }

  return { missing, tailwind, hardcolor };
}

function report(result) {
  let failed = 0;
  if (result.missing.length) {
    failed++;
    process.stdout.write(`FAIL  assert1 class-diff: ${result.missing.length} referenced class(es) have no CSS definition:\n`);
    for (const item of result.missing) process.stdout.write(`  ${item.name}  <- ${item.where.join(", ")}\n`);
  } else {
    process.stdout.write("PASS  assert1 class-diff: every className reference resolves to a definition\n");
  }
  if (result.tailwind.length) {
    failed++;
    process.stdout.write(`FAIL  assert2 tailwind-shaped classes (project has no Tailwind — these style nothing):\n`);
    for (const line of result.tailwind) process.stdout.write(`  ${line}\n`);
  } else {
    process.stdout.write("PASS  assert2 tailwind: no Tailwind-shaped classes outside the exempt files\n");
  }
  if (result.hardcolor.length) {
    failed++;
    process.stdout.write(`FAIL  assert3 hardcoded colors bypassing tokens (needs var() mix or whitelist):\n`);
    for (const line of result.hardcolor) process.stdout.write(`  ${line}\n`);
  } else {
    process.stdout.write("PASS  assert3 hardcoded-color: no new token-bypassing colors\n");
  }
  process.stdout.write("PASS  assert4 exemptions: template-literal classNames and test files are invisible to asserts 1-2 by construction\n");
  return failed;
}

// ── Negative self-test: prove each assertion actually blocks ───────────────
function selfTest() {
  const cases = [];
  const dir = mkdtempSync(path.join(tmpdir(), "task578-class-contract-"));
  try {
    // Each case gets its own subdirectory: a scan sees everything under the
    // root it is pointed at, so earlier fixtures must not leak into later ones.
    const caseDir = (name) => {
      const full = path.join(dir, name);
      fs.mkdirSync(full);
      fs.writeFileSync(path.join(full, "probe.css"), ".probe-ok578 { color: var(--fg); }\n");
      return full;
    };

    let d = caseDir("missing");
    fs.writeFileSync(path.join(d, "missing.tsx"), 'export const A = () => <button className="probe-ok578 zz578-undefined-probe" />;\n');
    cases.push(["assert1 blocks new undefined class", scan(d).missing.some((m) => m.name === "zz578-undefined-probe")]);

    d = caseDir("tailwind");
    fs.writeFileSync(path.join(d, "tailwind.tsx"), 'export const B = () => <button className="probe-ok578 rounded-full" />;\n');
    cases.push(["assert2 blocks tailwind-shaped class", scan(d).tailwind.some((t) => t.includes("rounded-full"))]);

    d = caseDir("hardcolor");
    fs.writeFileSync(path.join(d, "hard.css"), ".probe-hard578 { color: #123456; }\n");
    fs.writeFileSync(path.join(d, "hard.tsx"), 'export const C = () => <button className="probe-hard578" />;\n');
    cases.push(["assert3 blocks token-bypassing hardcoded color", scan(d).hardcolor.some((h) => h.includes(".probe-hard578"))]);

    d = caseDir("dynamic");
    fs.writeFileSync(path.join(d, "dynamic.tsx"), "export const D = (x: string) => <button className={`probe-ok578 dyn-${x}`} />;\n");
    const dynamic = scan(d);
    cases.push(["assert4 template-literal classNames stay exempt",
      !dynamic.missing.some((m) => m.name.startsWith("dyn-")) && dynamic.tailwind.length === 0]);

    let ok = true;
    for (const [label, passed] of cases) {
      process.stdout.write(`  ${passed ? "PASS" : "FAIL"}  ${label}\n`);
      if (!passed) ok = false;
    }
    return ok;
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

// ── Entry ──────────────────────────────────────────────────────────────────
const mode = process.argv[2];
if (mode === "--self-test") {
  process.exit(selfTest() ? 0 : 1);
} else if (mode === "--emit-baseline") {
  const result = scan(sourceRoot);
  const payload = {
    BASELINE_WHITELIST: result.missing.map((m) => m.name).sort(),
    TAILWIND_EXEMPT_FILES: [...new Set(result.tailwind.map((t) => t.split(":")[0]))].sort(),
    HARDCOLOR_WHITELIST: [...result.hardcolor.map((h) => h.replace(/^[^ ]+ /, "").replace(/\s+/g, " "))].sort(),
  };
  process.stdout.write(JSON.stringify(payload, null, 2) + "\n");
} else if (mode === undefined) {
  const failed = report(scan(sourceRoot));
  if (failed > 0) {
    process.stdout.write(`\n${failed} of 3 assertions failed. New references need real definitions — never whitelist-by-default.\n`);
    process.exit(1);
  }
} else {
  process.stderr.write(`unknown mode: ${mode}\n`);
  process.exit(2);
}
