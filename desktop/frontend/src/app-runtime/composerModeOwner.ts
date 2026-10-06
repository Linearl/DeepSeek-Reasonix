import { composerProfileWithMode, type ComposerProfile, type ComposerProfileField } from "../lib/composerProfile";
import { modeHasPlan, type CollaborationMode, type Mode, type ToolApprovalMode } from "../lib/types";
import type { SessionOperationAuthority, SessionResource } from "./useSessionOperations";

export type ComposerModeRequest =
  | { kind: "mode"; mode: Mode }
  | { kind: "collaboration"; mode: CollaborationMode }
  | { kind: "approval"; mode: ToolApprovalMode };
export type ComposerModePorts = {
  setMode: (tabId: string, mode: Mode) => Promise<void> | void;
  setCollaboration: (tabId: string, mode: CollaborationMode) => Promise<void>;
  setApproval: (tabId: string, mode: ToolApprovalMode) => Promise<void> | void;
  clearGoal: (tabId: string) => Promise<void>;
  setRemote: (tabId: string, collaboration: CollaborationMode, approval: ToolApprovalMode, goal: string) => Promise<string[]>;
  drainRemote: (tabId: string, ids: string[]) => void;
  patch: (tabId: string, patch: Partial<Omit<ComposerProfile, "pending">>, fields: ComposerProfileField[]) => void;
  rememberPlan: (tabId: string, enabled: boolean) => void;
  rememberApproval: (tabId: string, previous: ToolApprovalMode, next: ToolApprovalMode) => void;
};
export type ComposerModeInput = {
  target: SessionResource;
  request: ComposerModeRequest;
  remote: boolean;
  collaborationMode: CollaborationMode;
  toolApprovalMode: ToolApprovalMode;
  /** Raw first-axis autopilot flag (task 465 two-axis matrix). */
  autopilot: boolean;
  goal: string;
  ports: ComposerModePorts;
};

export async function executeComposerMode(input: ComposerModeInput, authority: SessionOperationAuthority): Promise<void> {
  const { target: { tabId }, request, ports } = input;
  authority.checkpoint();
  let patch: Partial<Omit<ComposerProfile, "pending">>;
  let fields: ComposerProfileField[];
  if (request.kind === "mode") {
    patch = composerProfileWithMode(request.mode);
    fields = ["collaborationMode", "toolApprovalMode", "goal"];
    if (input.remote) {
      const ids = await ports.setRemote(tabId, patch.collaborationMode ?? "normal", patch.toolApprovalMode ?? "ask", "");
      authority.checkpoint();
      if (authority.ownsUI()) ports.drainRemote(tabId, ids);
    } else await ports.setMode(tabId, request.mode);
    authority.checkpoint();
    ports.rememberPlan(tabId, modeHasPlan(request.mode));
  } else if (request.kind === "collaboration" && request.mode === "autopilot") {
    // Task 465 two-axis matrix: the autopilot tier lives on the FIRST axis
    // (approval posture). Switching to it implies yolo (the backend
    // auto-satisfies and records the decision) and must NOT clear the second
    // axis — goal × autopilot is a legal product state, so the goal stays
    // untouched and only the first-axis fields move.
    const nextLabel: CollaborationMode = input.collaborationMode === "plan" ? "plan" : input.goal.trim() ? "goal" : "autopilot";
    patch = { collaborationMode: nextLabel, toolApprovalMode: "yolo", autopilot: true, goalDraftMode: false };
    fields = ["collaborationMode", "toolApprovalMode", "autopilot"];
    if (input.remote) {
      const ids = await ports.setRemote(tabId, "autopilot", "yolo", "");
      authority.checkpoint();
      if (authority.ownsUI()) ports.drainRemote(tabId, ids);
    } else {
      await ports.setCollaboration(tabId, "autopilot");
    }
    authority.checkpoint();
    ports.rememberPlan(tabId, false);
  } else if (request.kind === "collaboration") {
    const mode = request.mode === "goal" ? "normal" : request.mode;
    patch = { collaborationMode: mode, goalDraftMode: request.mode === "goal", goal: "" };
    fields = ["collaborationMode", "goal"];
    if (input.remote) {
      const ids = await ports.setRemote(tabId, mode, input.toolApprovalMode, "");
      authority.checkpoint();
      if (authority.ownsUI()) ports.drainRemote(tabId, ids);
    } else {
      if (input.goal.trim()) {
        await ports.clearGoal(tabId);
        authority.checkpoint();
      }
      await ports.setCollaboration(tabId, mode);
    }
    authority.checkpoint();
    ports.rememberPlan(tabId, request.mode === "plan");
  } else {
    patch = { toolApprovalMode: request.mode };
    fields = ["toolApprovalMode"];
    // Task 465: leaving yolo while autopilot is on turns autopilot off
    // backend-side (325 reverse linkage, fail-closed) — mirror that in the
    // optimistic profile so the mode bar drops the tier immediately.
    if (input.autopilot && request.mode !== "yolo") {
      patch.autopilot = false;
      fields = ["toolApprovalMode", "autopilot"];
    }
    if (input.remote) {
      const mode = input.goal.trim() ? "goal" : input.collaborationMode === "plan" ? "plan" : "normal";
      const ids = await ports.setRemote(tabId, mode, request.mode, input.goal);
      authority.checkpoint();
      if (authority.ownsUI()) ports.drainRemote(tabId, ids);
    } else await ports.setApproval(tabId, request.mode);
    authority.checkpoint();
    ports.rememberApproval(tabId, input.toolApprovalMode, request.mode);
  }
  authority.checkpoint();
  ports.patch(tabId, patch, fields);
}
