// tl1547 (A1/A3): connection display names and per-model display names used to
// resolve only inside the settings panel (providerViewLabel → providerLabel.ts
// contract), while the session header trigger, the sidebar row hover and the
// model switcher kept rendering raw routing refs (`deepseek/deepseek-v4-flash`)
// — a user renaming a connection to 「我的DeepSeek」 then saw two names for the
// same thing on one screen.
//
// This module is the display-name seam for every surface that holds a model
// REF instead of a provider view: it keeps a tiny registry fed from the model
// catalog (ModelInfo.providerLabel = the connection's display label,
// ModelInfo.displayName = the per-model display name), and resolves
//
//   provider segment → registry > providerDefaultLabel (built-ins) > raw name
//   model segment    → registry (ref) > raw model id
//
// The registry is display-only: routing identity (provider.name, refs) is
// never rewritten, mirroring the providerLabel.ts contract comment. Surfaces
// without a catalog response yet degrade to providerDefaultLabel — a built-in
// label, never a regression to a raw id where one was already shown.

import { providerDefaultLabel } from "./providerLabel";

export interface ModelRefLabelInput {
  provider?: string;
  /** The connection's display label (settings displayName), backend-stamped. */
  providerLabel?: string;
  ref?: string;
  /** The per-model display name (model-level override), backend-stamped. */
  displayName?: string;
  model?: string;
}

const connectionLabels = new Map<string, string>();
const modelDisplayNames = new Map<string, string>();

/** Feed the registry from a model catalog snapshot. The snapshot is the whole
 *  truth: entries for connections/models that no longer appear in it are
 *  dropped (tl1654r2), so a deleted or disabled connection's display label
 *  cannot keep surfacing through stale refs (session/branch model refs that
 *  still point at it) after the config no longer lists the provider.
 *  Display labels only; the map stays tiny (one entry per ref). */
export function registerModelRefLabels(models: ModelRefLabelInput[]): void {
  const nextConnections = new Map<string, string>();
  const nextDisplayNames = new Map<string, string>();
  for (const m of models) {
    const provider = m.provider?.trim();
    const providerLabel = m.providerLabel?.trim();
    if (provider && providerLabel) nextConnections.set(provider, providerLabel);
    const ref = m.ref?.trim();
    const displayName = m.displayName?.trim();
    if (ref && displayName) nextDisplayNames.set(ref, displayName);
  }
  connectionLabels.clear();
  for (const [provider, label] of nextConnections) connectionLabels.set(provider, label);
  modelDisplayNames.clear();
  for (const [ref, displayName] of nextDisplayNames) modelDisplayNames.set(ref, displayName);
}

/** The label a user-facing surface shows for one connection: its registry
 *  display name when the catalog has one, else the providerLabel.ts default
 *  ladder (built-in vendor label > raw name). */
export function providerConnectionLabel(provider: string): string {
  const name = provider.trim();
  if (!name) return "";
  return connectionLabels.get(name) || providerDefaultLabel(name);
}

/** `provider/model` (or any model id) → the display form: the provider segment
 *  resolves through providerConnectionLabel, the model segment through the
 *  registry's per-model display name. A ref without a "/" is returned
 *  display-resolved as a whole (legacy single-segment values). */
export function modelRefDisplayLabel(ref: string): string {
  const trimmed = ref.trim();
  if (!trimmed) return "";
  const slash = trimmed.indexOf("/");
  if (slash <= 0) return modelDisplayNames.get(trimmed) || trimmed;
  const provider = providerConnectionLabel(trimmed.slice(0, slash));
  const model = trimmed.slice(slash + 1);
  const modelName = modelDisplayNames.get(trimmed) || model;
  return `${provider}/${modelName}`;
}
