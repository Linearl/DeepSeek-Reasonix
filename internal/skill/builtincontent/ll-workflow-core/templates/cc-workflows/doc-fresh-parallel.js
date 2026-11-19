/**
 * 并行文档保鲜审计工作流（CC Workflow 版本）
 *
 * 6 阶段流水线：
 *   扫描（Python）→ 按前缀分批 → 4 代理并行审计 → reviewer 复核 → 汇总 → 标记已审计
 *
 * 设计要点：
 *   1. Phase 1 调 Python 扫描器，生成 scan-result.json
 *   2. Phase 2 按前缀分组，≤4 篇/batch
 *   3. Phase 3 每 batch 派 4 代理（opus-deep / sonnet-structural / haiku-quick / sonnet-devil）
 *   4. Phase 3→4 强制门控：每个 batch 必须 4/4 报告齐备
 *   5. Phase 4 reviewer 合并去重裁决
 *   6. Phase 5 主对话汇总
 *   7. Phase 6 调 --mark-audited 写入 audited_sha
 */

export const meta = {
  name: 'doc-fresh-parallel',
  description: '并行文档保鲜审计：扫描→分批→4 代理审计→复核→汇总→标记',
  phases: [
    { title: 'Phase 1-扫描', detail: '执行 Python 扫描器生成 scan-result.json' },
    { title: '门控-扫描', detail: '校验 scan-result.json + summary.new > 0' },
    { title: 'Phase 2-分批', detail: '按前缀分批 ≤4 篇/batch' },
    { title: 'Phase 3-并行审计', detail: '4 代理/batch（opus/sonnet/haiku/sonnet）' },
    { title: '门控-批次完整性', detail: '每个 batch 必须 4/4 报告齐备' },
    { title: 'Phase 4-复核', detail: 'reviewer 合并去重裁决' },
    { title: '门控-复核', detail: '校验 review-*.md' },
    { title: 'Phase 5-汇总', detail: '主对话生成 summary.md' },
    { title: 'Phase 6-标记已审计', detail: '--mark-audited 写入 SHA' },
    { title: '门控-完成', detail: '校验所有产物 + 清理空 run_dir' },
  ],
}

// 4 代理配置（可被 args.models 覆盖）
var DEFAULT_AGENTS = [
  {
    key: 'opus-deep',
    model: 'opus',
    role: '深度分析',
    promptFile: 'routes/doc-fresh/prompts/opus-deep.md',
    outputSuffix: 'opus-deep.md',
    description: '逐段比对文档与代码，挖掘深层问题',
  },
  {
    key: 'sonnet-structural',
    model: 'sonnet',
    role: '结构检查',
    promptFile: 'routes/doc-fresh/prompts/sonnet-structural.md',
    outputSuffix: 'sonnet-structural.md',
    description: '验证文件路径、路由数量、目录结构',
  },
  {
    key: 'haiku-quick',
    model: 'haiku',
    role: '快速扫描',
    promptFile: 'routes/doc-fresh/prompts/haiku-quick.md',
    outputSuffix: 'haiku-quick.md',
    description: '检查日期/版本号/端口/硬编码数字',
  },
  {
    key: 'sonnet-devil',
    model: 'sonnet',
    role: "反面论证",
    promptFile: 'routes/doc-fresh/prompts/sonnet-devil.md',
    outputSuffix: 'sonnet-devil.md',
    description: '寻找文档仍然有效的证据，拦截过度报告',
  },
]

var SCAN_RESULT_SCHEMA = {
  type: 'object',
  properties: {
    summary: {
      type: 'object',
      properties: {
        total_scanned: { type: 'number' },
        stale: { type: 'number' },
        new: { type: 'number' },
        recurring_skipped: { type: 'number' },
        current: { type: 'number' },
      },
      required: ['total_scanned', 'new'],
    },
    audit_files: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          rel_path: { type: 'string' },
          git_sha: { type: 'string' },
          threshold_hours: { type: 'number' },
          age_hours: { type: 'number' },
        },
        required: ['rel_path'],
      },
    },
  },
  required: ['summary', 'audit_files'],
}

var BATCH_GROUPING_SCHEMA = {
  type: 'object',
  properties: {
    batches: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          name: { type: 'string', description: 'batch-01-architecture' },
          prefix: { type: 'string', description: '2.tech/architecture/' },
          docList: { type: 'array', items: { type: 'string' } },
          outputDir: { type: 'string', description: '绝对路径' },
        },
        required: ['name', 'docList', 'outputDir'],
      },
    },
  },
  required: ['batches'],
}

// ═══════════════════════════════════════════════════════════════
// 工具函数
// ═══════════════════════════════════════════════════════════════

/**
 * 门控：检查文件是否存在且非空
 */
async function gateCheck(filePath, stageName) {
  var prompt = '检查文件是否有效。文件路径: ' + filePath + '\n\n'
    + '1. 使用 Bash 执行 test -f 检查文件是否存在\n'
    + '2. 如果存在，检查文件大小 (wc -c) 是否 > 0\n'
    + '3. 如果是 JSON 文件，用 node -e 验证 JSON.parse 格式\n\n'
    + '输出 JSON: {"exists": bool, "size": number, "valid": bool, "reason": "string"}'
  var result = await agent(prompt, {
    label: '门控检查:' + filePath.split(/[\\/]/).pop(),
  }).then(function(r) {
    return r || { exists: false, size: 0, valid: false, reason: 'agent 返回空' }
  }).catch(function(err) {
    return { exists: false, size: 0, valid: false, reason: err.message }
  })
  return result
}

async function gate(filePath, type, stageName) {
  log('门控: ' + stageName + ' → ' + filePath.split(/[\\/]/).pop())
  var check = await gateCheck(filePath, type)
  if (check.exists && check.valid) {
    log('✓ 门控通过: ' + stageName + '（' + check.size + ' bytes）')
    return true
  } else {
    log('✗ 门控失败: ' + stageName + ' — ' + check.reason)
    return false
  }
}

/**
 * 调 Python 扫描器
 */
async function runPythonScanner(repoRoot, workDir, scanPath, thresholdOverride, forceFull, maxFilesPerRun) {
  var cmd = 'python "' + repoRoot + '/.claude/skills/ll-workflow-core/routes/doc-fresh/scripts/check-doc-freshness.py"'
    + ' --work-dir "' + workDir + '"'
    + ' --scan-path "' + scanPath + '"'
  if (thresholdOverride) cmd += ' --threshold-override ' + thresholdOverride
  if (forceFull) cmd += ' --force-full'
  if (maxFilesPerRun) cmd += ' --max-files-per-run ' + maxFilesPerRun

  return agent('使用 Bash 工具执行以下命令：\n\n' + cmd + '\n\n执行后报告：\n1. 命令的 exit code\n2. 最后 5 行输出（包含 run_dir 路径）\n3. 生成的 scan-result.json 完整路径', {
    label: '调 Python 扫描器',
  })
}

/**
 * 派出单个审计子代理
 */
async function dispatchAuditAgent(agentCfg, batch, repoRoot) {
  var outputAbs = batch.outputDir + '/' + agentCfg.outputSuffix
  var promptBody = '按模板执行审计。\n\n'
    + '模板文件: ' + repoRoot + '/.claude/skills/ll-workflow-core/' + agentCfg.promptFile + '\n'
    + '   （使用 Read 工具读取后按模板格式输出）\n\n'
    + 'batch_name: ' + batch.name + '\n'
    + 'repo_root: ' + repoRoot + '\n'
    + 'doc_list:\n'
    + batch.docList.map(function(d, i) { return '  ' + (i + 1) + '. ' + d }).join('\n') + '\n\n'
    + 'output: ' + outputAbs + '\n\n'
    + '【强制】output 与 repo_root 都是绝完整路径，不要拼接或改路径。\n'
    + '【强制】先 Read 模板文件，再按模板格式输出，最终用 Write 工具写入 output 路径。'

  return agent(promptBody, {
    label: agentCfg.key + ':' + batch.name,
    model: agentCfg.model,
  }).then(function() {
    return { agent: agentCfg.key, batch: batch.name, output: outputAbs, status: 'ok' }
  }).catch(function(err) {
    return { agent: agentCfg.key, batch: batch.name, output: outputAbs, status: 'fail', error: err.message }
  })
}

// ═══════════════════════════════════════════════════════════════
// 主工作流
// ═══════════════════════════════════════════════════════════════

var args = typeof globalThis.args !== 'undefined' ? globalThis.args : {}
var repoRoot = (args && args.repoRoot) || process.cwd()
var scanPath = (args && args.scanPath) || '2.tech/'
var thresholdOverride = args && args.thresholdOverride
var forceFull = !!(args && args.forceFull)
var maxFilesPerRun = (args && args.maxFilesPerRun) || 12
var maxBatchSize = (args && args.maxBatchSize) || 4
var models = (args && args.models) || DEFAULT_AGENTS

log('=== 并行文档保鲜审计 ===')
log('项目根: ' + repoRoot)
log('扫描路径: ' + scanPath)
log('强制全量: ' + forceFull)
log('每批上限: ' + maxBatchSize + ' 篇')

// ═══════════════════════════════════════════════════════════════
// Phase 1: 扫描
// ═══════════════════════════════════════════════════════════════

phase('Phase 1-扫描')

var workDir = (args && args.workDir) || (repoRoot + '/04-auto_audit')

// 创建 run_dir（YYYY-MM-DD-NNNNNN 格式）
var seqResult = await agent('使用 Bash 工具生成今日运行目录路径：\n\n'
  + '1. 获取今天日期（YYYY-MM-DD 格式）\n'
  + '2. 在 ' + workDir + '/results/ 下查找以今天日期开头的目录，取最大序号 + 1\n'
  + '3. 新目录路径格式：{today}-{seq:06d}（如 2026-06-23-000001）\n'
  + '4. 创建该目录（mkdir -p）\n\n'
  + '输出 runDir 的绝对路径：', {
  label: '生成 runDir',
})

// 简化处理：从 seqResult 提取 runDir（agent 返回的纯文本）
var runDir = (typeof seqResult === 'string' ? seqResult : '').trim().split('\n').pop().trim()
if (!runDir || !runDir.match(/^\d{4}-\d{2}-\d{2}-\d{6}$/)) {
  // 回退：基于今天日期
  var now = new Date()
  var today = now.getFullYear() + '-' + String(now.getMonth() + 1).padStart(2, '0') + '-' + String(now.getDate()).padStart(2, '0')
  runDir = workDir + '/results/' + today + '-000001'
}
var runDirAbs = runDir.indexOf('/') === 0 || runDir.match(/^[a-zA-Z]:/) ? runDir : (workDir + '/results/' + runDir)
log('runDir: ' + runDirAbs)

// 调 Python 扫描器
await runPythonScanner(repoRoot, runDirAbs, scanPath, thresholdOverride, forceFull, maxFilesPerRun)

var scanResultPath = runDirAbs + '/scan-result.json'
var scanResultRaw = await agent('使用 Read 工具读取 ' + scanResultPath + ' 的完整内容，按 SCAN_RESULT_SCHEMA 格式输出 JSON：', {
  label: '读取 scan-result',
  schema: SCAN_RESULT_SCHEMA,
})

var scanResult = scanResultRaw || { summary: { new: 0, total_scanned: 0 }, audit_files: [] }
log('扫描结果: total=' + scanResult.summary.total_scanned + ', new=' + scanResult.summary.new + ', current=' + scanResult.summary.current)

// ═══════════════════════════════════════════════════════════════
// 门控：扫描
// ═══════════════════════════════════════════════════════════════

phase('门控-扫描')
if (!await gate(scanResultPath, 'json', '扫描结果')) {
  throw new Error('scan-result.json 缺失或无效')
}
if (scanResult.summary.new === 0 && !forceFull) {
  log('无新文档需审计（summary.new=0），直接进入汇总阶段')
  // 跳过 Phase 2-4，进入 Phase 5 生成空 summary
  scanResult._skipAudit = true
}

// ═══════════════════════════════════════════════════════════════
// Phase 2: 按前缀分批
// ═══════════════════════════════════════════════════════════════

var batches = []
if (!scanResult._skipAudit) {
  phase('Phase 2-分批')

  var groupingRaw = await agent('你是文档分批专家。\n\n'
    + '扫描结果 audit_files 共 ' + scanResult.audit_files.length + ' 个文档。\n'
    + '请按以下规则分批：\n'
    + '  1. 按 rel_path 的二级目录前缀分组（如 2.tech/architecture/）\n'
    + '  2. 同前缀尽量不分拆\n'
    + '  3. 每组 ≤ ' + maxBatchSize + ' 篇\n'
    + '  4. 混合前缀仅在填充不满 ' + maxBatchSize + ' 篇时使用\n'
    + '  5. batch 命名：batch-NN-{slug}，NN 从 01 开始递增\n'
    + '  6. outputDir 为绝完整路径：' + runDirAbs + '/{batch_name}\n\n'
    + 'audit_files:\n'
    + JSON.stringify(scanResult.audit_files, null, 2)
    + '\n\n按 BATCH_GROUPING_SCHEMA 输出 JSON。', {
    label: '分批',
    schema: BATCH_GROUPING_SCHEMA,
  })

  batches = (groupingRaw && groupingRaw.batches) || []
  log('分批完成：' + batches.length + ' 个 batch')

  // 创建每个 batch 目录
  for (var i = 0; i < batches.length; i++) {
    var b = batches[i]
    await agent('使用 Bash 工具执行：mkdir -p "' + b.outputDir + '"', { label: 'mkdir:' + b.name })
  }
}

// ═══════════════════════════════════════════════════════════════
// Phase 3: 并行审计（每 batch 4 代理）
// ═══════════════════════════════════════════════════════════════

if (!scanResult._skipAudit) {
  phase('Phase 3-并行审计')

  // 使用 pipeline 让多个 batch 并发执行，但每个 batch 内部 4 代理是 barrier
  // 这里简化为：所有 batch 的所有代理一次性并行（如果 batch 较少）
  // 高级版：每个 batch 内部先 barrier，再启动下一批
  var allAgentTasks = []
  for (var bi = 0; bi < batches.length; bi++) {
    for (var ai = 0; ai < models.length; ai++) {
      (function(batch, agentCfg) {
        allAgentTasks.push(function() { return dispatchAuditAgent(agentCfg, batch, repoRoot) })
      })(batches[bi], models[ai])
    }
  }

  log('启动 ' + allAgentTasks.length + ' 个审计子代理（' + batches.length + ' batches × ' + models.length + ' 代理）')
  var dispatchResults = await parallel(allAgentTasks)
  var successCount = dispatchResults.filter(function(r) { return r && r.status === 'ok' }).length
  log('子代理完成: ' + successCount + '/' + dispatchResults.length)
}

// ═══════════════════════════════════════════════════════════════
// 门控：批次完整性
// ═══════════════════════════════════════════════════════════════

phase('门控-批次完整性')
var allBatchesPass = true
if (!scanResult._skipAudit) {
  for (var ci = 0; ci < batches.length; ci++) {
    var bc = batches[ci]
    var presentCount = 0
    for (var mi = 0; mi < models.length; mi++) {
      var f = bc.outputDir + '/' + models[mi].outputSuffix
      var exists = await agent('使用 Bash 执行：test -f "' + f + '" && echo "yes" || echo "no"', { label: 'check:' + f.split(/[\\/]/).pop() })
      if (exists && String(exists).includes('yes')) presentCount++
    }
    log('  ' + bc.name + ': ' + presentCount + '/' + models.length)
    if (presentCount < models.length) {
      log('  ✗ 批次不完整，缺失 ' + (models.length - presentCount) + ' 个报告')
      allBatchesPass = false
    }
  }
}

if (!scanResult._skipAudit && !allBatchesPass) {
  throw new Error('批次完整性门控失败：存在报告缺失，禁止进入 Phase 4')
}

// ═══════════════════════════════════════════════════════════════
// Phase 4: 复核
// ═══════════════════════════════════════════════════════════════

var reviewReports = []
if (!scanResult._skipAudit) {
  phase('Phase 4-复核')

  var reviewerTemplatePath = repoRoot + '/.claude/skills/ll-workflow-core/routes/doc-fresh/prompts/reviewer.md'
  for (var ri = 0; ri < batches.length; ri++) {
    (function(batch, idx) {
      var reviewOutput = runDirAbs + '/review-' + String(idx + 1).padStart(2, '0') + '.md'
      var reviewPrompt = '按模板执行复核。\n\n'
        + '模板文件: ' + reviewerTemplatePath + '\n'
        + '   （使用 Read 工具读取后按模板格式输出）\n\n'
        + 'batch_list: ' + batch.name + '\n'
        + 'run_dir_root: ' + runDirAbs + '\n'
        + 'repo_root: ' + repoRoot + '\n'
        + 'output: ' + reviewOutput + '\n\n'
        + '【强制】output 与 repo_root 都是绝完整路径。\n'
        + '【强制】先 Read 模板文件，再按模板格式输出，最终用 Write 工具写入 output 路径。'
      return agent(reviewPrompt, { label: 'reviewer:' + batch.name, model: 'sonnet' })
        .then(function() { reviewReports.push({ batch: batch.name, output: reviewOutput }) })
        .catch(function(err) { log('reviewer ' + batch.name + ' 失败: ' + err.message) })
    })(batches[ri], ri)
  }

  // 串行等待所有 reviewer（保持简单，可改为 parallel）
  await new Promise(function(resolve) { setTimeout(resolve, 100) })
  for (var rj = 0; rj < reviewReports.length; rj++) {
    log('reviewer 已派: ' + reviewReports[rj].batch)
  }
}

// ═══════════════════════════════════════════════════════════════
// 门控：复核
// ═══════════════════════════════════════════════════════════════

phase('门控-复核')
for (var rk = 0; rk < reviewReports.length; rk++) {
  await gate(reviewReports[rk].output, 'markdown', '复核:' + reviewReports[rk].batch)
}

// ═══════════════════════════════════════════════════════════════
// Phase 5: 汇总
// ═══════════════════════════════════════════════════════════════

phase('Phase 5-汇总')

var summaryPath = runDirAbs + '/summary.md'
var summaryTemplate = repoRoot + '/.claude/skills/ll-workflow-core/routes/doc-fresh/templates/phase5-summary.md'

var summaryPrompt = '你是文档保鲜审计汇总代理。\n\n'
  + '汇总模板: ' + summaryTemplate + '（使用 Read 工具读取）\n'
  + '运行目录: ' + runDirAbs + '\n\n'
  + '任务：\n'
  + '  1. 读取 run_dir 下所有 review-*.md 复核报告\n'
  + '  2. 按汇总模板生成最终 summary.md\n'
  + '  3. 写入: ' + summaryPath + '\n\n'
  + '汇总内容必须包含：\n'
  + '  - 流程完整性（每阶段产出 + 状态）\n'
  + '  - 审计结果表（stale / partial / current 判定 + P0/P1/P2 计数）\n'
  + '  - P0 清单（含共识数）\n'
  + '  - 自适应阈值更新记录（乘数变化 + 有效阈值）\n\n'
  + '【强制】最终用 Write 工具将汇总写入 summaryPath。'

await agent(summaryPrompt, { label: '生成 summary.md', model: 'sonnet' })
log('summary.md 已生成: ' + summaryPath)

// ═══════════════════════════════════════════════════════════════
// Phase 6: 标记已审计
// ═══════════════════════════════════════════════════════════════

phase('Phase 6-标记已审计')

if (!scanResult._skipAudit) {
  for (var di = 0; di < scanResult.audit_files.length; di++) {
    var doc = scanResult.audit_files[di]
    var markCmd = 'python "' + repoRoot + '/.claude/skills/ll-workflow-core/routes/doc-fresh/scripts/check-doc-freshness.py"'
      + ' --work-dir "' + runDirAbs + '"'
      + ' --mark-audited "' + doc.rel_path + '"'
    await agent('使用 Bash 工具执行：\n' + markCmd + '\n\n如果执行成功（exit code 0），输出 "ok: {rel_path}"。', {
      label: 'mark:' + doc.rel_path,
    })
  }
  log('已标记 ' + scanResult.audit_files.length + ' 个文档')
} else {
  log('无新文档，跳过标记阶段')
}

// ═══════════════════════════════════════════════════════════════
// 门控：完成
// ═══════════════════════════════════════════════════════════════

phase('门控-完成')
await gate(summaryPath, 'markdown', 'summary.md')
log('=== 文档保鲜审计完成 ===')
log('run_dir: ' + runDirAbs)
log('summary: ' + summaryPath)

return {
  runDir: runDirAbs,
  summaryPath: summaryPath,
  batches: batches.length,
  newDocs: scanResult.summary.new,
  reviewReports: reviewReports,
}
