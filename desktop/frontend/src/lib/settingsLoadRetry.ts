// tl 1307: 设置读失败 ≠ 终态。
//
// 20261031 20-21 时段实证：后端零 ERROR，只有 settings stage slow 多段叠加
// （user-config 61-99ms / merged-write-roots 132-336ms / provider-views
// 61-204ms）+ 同窗 session save-path lock waited 高频（21:51-21:56 每
// 10-30s 一次，大会话保存 + events 超限告警）——系统处于锁竞争窗口时，
// 设置读的单次请求可能拒绝或被显著拖慢。旧实现一击即判失败，弹
// 「设置加载失败。」横幅要用户手动点重试；实际锁窗口毫秒~秒级就过去，
// 重发一次大概率就成功。
//
// 本模块只持有策略：有上限（delaysMs.length 次）+ 有观测（调用方对每次
// 重试与终态失败逐条 reportFrontendLog），不做请求本体——请求生命周期
// （seq 作废、预取消费、骨架保持）仍归 SettingsPanel.reload。
export const settingsLoadRetry = {
	/** 第 n 次自动重试前的退避等待（ms）。数组长度 = 最大自动重试次数
	 *  （当前 2 次：首试 + 2 次重试 = 3 次尝试封顶）。测试可改写以避免
	 *  真实退避等待；生产语义：退避 400ms/1200ms 足以跨过实测的锁尖峰
	 *  （stage slow 单段 ≤400ms 量级）。 */
	delaysMs: [400, 1200] as number[],
};
