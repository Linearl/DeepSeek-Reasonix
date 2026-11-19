#!/usr/bin/env node
/**
 * req-sync.js — 需求编号管理与 requirements.md 同步工具（入口）
 *
 * 功能：
 *   next    — 查询指定类别下 N 个可用需求编号
 *   query   — 查询某个编号的详细信息
 *   scan    — 扫描所有任务包，输出 JSON 快照（供 sync 使用）
 *   backfill — 为缺少 run_result.json 的任务包批量生成
 *   archive — 归档 B.todo 中 status=done/completed → B.2.done，cancelled → B.3.cancelled
 *   sync    — 根据扫描结果更新 requirements.md 表格（dry-run 默认开启）
 *
 * 用法：
 *   node req-sync.js next --category 7 [--count 5]
 *   node req-sync.js query 7076
 *   node req-sync.js scan [--git]
 *   node req-sync.js backfill [--apply]
 *   node req-sync.js archive [--apply]
 *   node req-sync.js sync [--apply] [--git]
 *
 * 实现拆分见 req-sync/ 子目录。
 * 零外部依赖，仅使用 Node.js 内置模块。
 */

const { parseArgs, showHelp } = require("./req-sync/utils.js");
const { cmdScan, cmdBackfill } = require("./req-sync/scan.js");
const { cmdSync } = require("./req-sync/sync.js");
const { cmdNext } = require("./req-sync/next.js");
const { cmdQuery } = require("./req-sync/query.js");
const { cmdArchive } = require("./req-sync/archive.js");

function main() {
  const args = parseArgs(process.argv);

  if (args.help) {
    showHelp();
    return;
  }

  switch (args.command) {
    case "help":
      showHelp();
      break;
    case "next":
      cmdNext(args);
      break;
    case "query":
      cmdQuery(args);
      break;
    case "scan":
      cmdScan(args);
      break;
    case "backfill":
      cmdBackfill(args);
      break;
    case "sync":
      cmdSync(args);
      break;
    case "archive":
      cmdArchive(args);
      break;
    default:
      showHelp();
      break;
  }
}

main();
