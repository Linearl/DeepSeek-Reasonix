// tl1415d9: 副本面板会话行「用户展开过的行」记忆(用户二轮裁决 20261107)。
//
// 裁决:折叠语义正解=刷新后不要收起——默认折叠只是「开面板时」的行为;用户
// 仍然展开了行,数据刷新/合并生效后应继续保持展开态(展开是用户状态,刷新
// 不重置)。红基线实测的两条丢失路径(本模块各钉一条):
//   ① 组件重挂载(设置面板关开/tab 回切/SectionBoundary 重试)——展开态原是
//     RecoveryCopiesSection 内的 useState,任何重挂载整体重置;
//   ② 采纳副本(切换主线)——展开态原按完整 mainPath 记,tl1430 语义下采纳
//     换的是主线**文件**(desktop/recovery_lineage.go:文件不搬、组 preferred
//     path 变成副本文件),旧 path 永久失配。
// 修法与 tl1439 面板记忆同模式:localStorage 单一真相源,挂载即读、toggle 即
// 写穿(应用刷新/重启后仍在)。特意的单源:首版模块级 Set 副本与 storage 双写
// 在测试里当场暴露漂移(清 storage 不清 Set)——同一语义两套真相源必有一套腐
// 化,读侧每次现读成本低(挂载/点击频度),不值得养缓存。只记「用户展开过的」
// ——未动过的行开面板仍默认折叠(裁决「默认收起语义不变」面)。
//
// 键 = 目录 + lineage stem(basename 去 .jsonl 再剥 -recovery-<16hex> 后缀),
// 全程小写(Windows 两个后端读数大小写漂移是 tl1414 实测过的坑,sameSessionPath
// 同款防误判方向:误合并键只会多恢复一次展开,无害;漏合并键才丢展开态)。
// stem 剥副本后缀后,采纳前(gc.jsonl)与采纳后(gc-recovery-<hex>.jsonl)映射到
// 同一键;合并不改主线文件,键不变。topicId 是更正统的标识,但副本组视图
// (RecoveryCopyGroupView)不携带,前端按目录+stem 推导与后端扫描的「同目录
// 同 stem 归组」是同一身份面,零桥接改动拿到同等的跨采纳稳定性。

const STORAGE_KEY = "recoveryCopies.expandedSessions";
// 防膨胀上限:面板会话量级是几十,512 远超实际;超限丢最老的(存储序=使用序)。
const MAX_KEYS = 512;

function readStoredKeys(): Set<string> {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return new Set();
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return new Set();
    return new Set(parsed.filter((k): k is string => typeof k === "string" && k.length > 0));
  } catch {
    // 读侧失败(损坏值/私密模式)回落空集=全折叠,面板照常出,只损失记忆。
    return new Set();
  }
}

/** sessionExpandKey maps a group's mainline path to the lineage-stable expand
 *  key. 采纳副本后主线 path 变了,键不变——展开态跟着会话走,不跟文件名走。 */
export function sessionExpandKey(mainPath: string): string {
  const normalized = mainPath.replaceAll("\\", "/").toLowerCase();
  const cut = normalized.lastIndexOf("/");
  const dir = cut >= 0 ? normalized.slice(0, cut) : "";
  const base = cut >= 0 ? normalized.slice(cut + 1) : normalized;
  const stem = base.replace(/\.jsonl$/, "").replace(/-recovery-[0-9a-f]{16}$/, "");
  return dir ? `${dir}|${stem}` : stem;
}

/** 面板挂载/重挂载时的初值:每次现读 storage(单一真相源,无模块缓存)。 */
export function readExpandedSessionKeys(): Set<string> {
  return readStoredKeys();
}

/** toggle 一把走完「读-改-写穿-返回新 Set」——组件不必在 setState updater
 *  里带副作用(StrictMode 双调 updater 也不会双写)。 */
export function toggleExpandedSessionKey(key: string): Set<string> {
  const keys = readStoredKeys();
  if (keys.has(key)) {
    keys.delete(key);
  } else {
    keys.add(key);
    if (keys.size > MAX_KEYS) {
      const oldest = keys.values().next().value;
      if (oldest !== undefined) keys.delete(oldest);
    }
  }
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify([...keys]));
  } catch {
    // 存储失败(私密模式等)只损失跨挂载记忆,本次会话内 state 照常工作。
  }
  return keys;
}
