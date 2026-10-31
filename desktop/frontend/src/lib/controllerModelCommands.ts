import { app } from "./bridge";
import { emitActiveModelChanged } from "./activeModelChanged";
import { beginEffortSwitch, endEffortSwitch } from "../store/effortApplying";
import type { BalanceInfo } from "./types";

type Ref<T> = { current: T };
type Ports = {
  statesRef: Ref<ReadonlyMap<string, { balance?: BalanceInfo }>>;
  modelSwitchSeqByTab: Ref<Map<string, number>>;
  modelSwitchSuccessVersionByTab: Ref<Map<string, number>>;
  modelSwitchQueueByTab: Ref<ReadonlyMap<string, { fallbackBalance?: BalanceInfo }>>;
  enqueueModelSwitch: (tabId: string, name: string, balance?: BalanceInfo) => Promise<"applied" | "superseded">;
  clearBalanceForTab: (tabId: string) => void;
  dispatchTo: (tabId: string, action: { type: "local_notice"; level: "warn"; text: string } | { type: "balance"; balance: BalanceInfo }) => void;
  refreshBalanceForTab: (tabId: string) => Promise<unknown>;
  refreshMetaForTab: (tabId: string) => Promise<void>;
};

/** Uses Controller-owned queues and stores; never resolves an active tab after await. */
export function createControllerModelCommands(ports: Ports) {
  const { statesRef, modelSwitchSeqByTab, modelSwitchSuccessVersionByTab, modelSwitchQueueByTab,
    enqueueModelSwitch, clearBalanceForTab, dispatchTo, refreshBalanceForTab, refreshMetaForTab } = ports;
  const setModelForTab = async (tabId: string, name: string) => {
    if (!tabId) return false;
    const switchSeq = (modelSwitchSeqByTab.current.get(tabId) ?? 0) + 1;
    const successVersion = modelSwitchSuccessVersionByTab.current.get(tabId) ?? 0;
    const existingQueue = modelSwitchQueueByTab.current.get(tabId);
    // Every attempt in one queued burst shares the balance that was visible
    // before the first switch cleared it. Otherwise a later queued failure
    // captures the placeholder and cannot restore the outgoing provider.
    const fallbackBalance = existingQueue
      ? existingQueue.fallbackBalance
      : statesRef.current.get(tabId)?.balance;
    modelSwitchSeqByTab.current.set(tabId, switchSeq);
    // Hide the outgoing provider's wallet as soon as the user starts a hot
    // switch. If the rebuild fails, the catch path re-queries the still-active
    // provider and restores its balance.
    clearBalanceForTab(tabId);
    try {
      const result = await enqueueModelSwitch(tabId, name, fallbackBalance);
      if (result === "superseded") return false;
      modelSwitchSuccessVersionByTab.current.set(
        tabId,
        (modelSwitchSuccessVersionByTab.current.get(tabId) ?? 0) + 1,
      );
    } catch (err) {
      if (modelSwitchSeqByTab.current.get(tabId) !== switchSeq) return false;
      const { modelSwitchNoticeText } = await import("./controllerSwitchNotices");
      if (modelSwitchSeqByTab.current.get(tabId) !== switchSeq) return false;
      dispatchTo(tabId, { type: "local_notice", level: "warn", text: modelSwitchNoticeText(err) });
      const olderSwitchSucceeded =
        (modelSwitchSuccessVersionByTab.current.get(tabId) ?? 0) !== successVersion;
      // Restore the known balance only when no older overlapping switch
      // completed after this attempt began. Otherwise the backend now owns a
      // different provider and the refresh below must establish its balance.
      if (fallbackBalance && !olderSwitchSucceeded) {
        dispatchTo(tabId, { type: "balance", balance: fallbackBalance });
      }
      void refreshBalanceForTab(tabId);
      // A superseded success deliberately skips its own UI reconciliation.
      // If this latest queued switch then fails, reconcile the model metadata
      // to the provider that actually became active in the backend.
      if (olderSwitchSucceeded) await refreshMetaForTab(tabId);
      // 任务 872：最新一次尝试失败落定（后端仍是旧 provider）——同样广播，
      // 让被作废的用量面重查并收敛回实际活跃的 provider；若落定前又被
      // 更新的尝试接管则不广播（由更新的那次负责）。
      if (modelSwitchSeqByTab.current.get(tabId) === switchSeq) emitActiveModelChanged();
      return false;
    }
    if (modelSwitchSeqByTab.current.get(tabId) !== switchSeq) return false;
    void refreshBalanceForTab(tabId);
    await refreshMetaForTab(tabId);
    const latest = modelSwitchSeqByTab.current.get(tabId) === switchSeq;
    // 任务 872：最新一次切换落定后广播「活跃模型已变」——用量显示面
    // （PlanUsageCard / OpenCodeGoUsageSidebarCard / 状态栏）同步作废旧
    // provider 快照并重查，否则旧卡要等 60s 轮询才下线，切换瞬间与新
    // provider 卡同屏并存。被更新的尝试 superseded 时不广播（由更新的
    // 那次落定广播）。
    if (latest) emitActiveModelChanged();
    return latest;
  };

  const setEffortForTab = async (tabId: string, level: string) => {
    if (!tabId) return;
    // Task 1301: the in-flight window brackets the backend call. The fallback
    // rebuild emits "effort:applying" mid-call (marked only inside this
    // window); when the call settles — applied or failed — the transition
    // must go, so the clear runs on both exit paths.
    beginEffortSwitch(tabId);
    try {
      await app.SetEffortForTab(tabId, level);
    } catch (err) {
      const { effortSwitchNoticeText } = await import("./controllerSwitchNotices");
      dispatchTo(tabId, { type: "local_notice", level: "warn", text: effortSwitchNoticeText(err) });
      return;
    } finally {
      endEffortSwitch(tabId);
    }
    await refreshMetaForTab(tabId);
  };


  return { setModelForTab, setEffortForTab };
}
