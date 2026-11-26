import type { StructuredInvocationSubmit } from "./invocationDisplay";

/**
 * Tab-scoped first Goal turn. The backend receives the source tab in one call,
 * so Goal activation and the structured Skill submit cannot be split by a tab
 * switch.
 */
export async function activateGoalAndSubmitOnTab({
  tabId,
  displayText,
  submitText,
  structured,
  sendToTab,
}: {
  tabId: string;
  displayText: string;
  submitText: string;
  structured?: StructuredInvocationSubmit;
  sendToTab: (
    tabId: string,
    goal: string,
    displayText: string,
    submitText: string,
    structured?: StructuredInvocationSubmit,
  ) => void | Promise<void>;
}): Promise<void> {
  const sourceTabId = tabId;
  const goal = displayText.trim();
  await sendToTab(
    sourceTabId,
    goal,
    goal,
    structured ? submitText.trim() : `/goal ${submitText.trim()}`,
    structured,
  );
}
