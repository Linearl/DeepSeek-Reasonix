/**
 * 并行审计工作流 v2 - 9 维度版本（优化版 v3）
 *
 * 优化：门控检查 + 复核并发3 + 超时保护 + 降级策略
 */

export const meta = {
  name: 'audit-parallel-v2',
  description: '并行代码审计 v2：门控驱动 + 分批扫描 + 全量复核 + 增量落盘',
  phases: [
    { title: '预处理', detail: '创建目录 + 并行调查' },
    { title: '门控-预处理', detail: '检查预处理产物' },
    { title: '并行扫描-批次1', detail: 'security, stability, architecture' },
    { title: '门控-批次1', detail: '检查扫描产物' },
    { title: '并行扫描-批次2', detail: 'quality, performance, testability' },
    { title: '门控-批次2', detail: '检查扫描产物' },
    { title: '并行扫描-批次3', detail: 'observability, maintainability, compatibility' },
    { title: '门控-批次3', detail: '检查扫描产物' },
    { title: '合并扫描', detail: '汇总全量 findings' },
    { title: '门控-合并', detail: '检查合并产物' },
    { title: '并行复核', detail: '全量问题验证（并发3）' },
    { title: '门控-复核', detail: '检查复核产物' },
    { title: '报告生成', detail: '评分 + Markdown 报告' },
    { title: '门控-报告', detail: '检查报告产物' },
    { title: '最终落盘', detail: '合并最终报告' },
    { title: '门控-完成', detail: '验证所有产物' },
  ],
}

// 审计维度定义
var AUDIT_DIMENSIONS = [
  { key: 'security', name: '安全性', weight: 0.2, prompt: '审计代码安全性：异常处理、输入验证、资源管理、认证授权、敏感数据' },
  { key: 'stability', name: '稳定性', weight: 0.15, prompt: '审计代码稳定性：并发安全、错误恢复、边界处理、状态管理、依赖健康' },
  { key: 'architecture', name: '架构设计', weight: 0.15, prompt: '审计架构设计：分层合理性、依赖管理、职责划分、扩展性、模块化' },
  { key: 'quality', name: '代码质量', weight: 0.15, prompt: '审计代码质量：可读性、函数设计、代码重复、魔法数字、死代码' },
  { key: 'performance', name: '性能效率', weight: 0.1, prompt: '审计性能效率：算法效率、内存使用、I/O优化、数据库、前端性能' },
  { key: 'testability', name: '可测试性', weight: 0.1, prompt: '审计可测试性：测试覆盖率、测试结构、Mock合理性、边界用例、E2E测试' },
  { key: 'observability', name: '可观测性', weight: 0.05, prompt: '审计可观测性：日志规范、错误上下文、健康检查、性能监控、审计日志' },
  { key: 'maintainability', name: '可维护性', weight: 0.05, prompt: '审计可维护性：文档完整性、配置管理、类型安全、代码注释、依赖管理' },
  { key: 'compatibility', name: '兼容性', weight: 0.05, prompt: '审计兼容性：依赖锁定、运行时版本、API兼容性、浏览器兼容性' },
]

var FINDINGS_SCHEMA = {
  type: 'object',
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          file: { type: 'string' },
          line: { type: 'number' },
          severity: { type: 'string', enum: ['P1', 'P2', 'P3', 'P4'] },
          title: { type: 'string' },
          description: { type: 'string' },
          evidence: { type: 'string', description: '简要证据：关键代码片段、grep 结果、或直接观察到的行为' },
          risk: { type: 'string' },
          suggestion: { type: 'string' },
        },
        required: ['file', 'severity', 'title', 'description', 'evidence'],
      },
    },
  },
  required: ['findings'],
}

var VERDICT_SCHEMA = {
  type: 'object',
  properties: {
    findingId: { type: 'string' },
    isConfirmed: { type: 'boolean' },
    isFalsePositive: { type: 'boolean' },
    confidence: { type: 'number' },
    reviewNote: { type: 'string' },
    evidence: { type: 'string', description: '复核证据：确认时给出验证代码/行为；推翻时给出反证（如"该路径已被 X 中间件保护"）' },
  },
  required: ['findingId', 'isConfirmed', 'evidence'],
}

// ═══════════════════════════════════════════════════════════════
// 门控工具函数
// ═══════════════════════════════════════════════════════════════

/**
 * 检查文件是否存在且有效（非空、可解析 JSON）
 * @param {string} filePath - 文件路径
 * @param {string} type - 文件类型：'json' | 'markdown' | 'any'
 * @returns {Promise<{pass: boolean, reason: string}>}
 */
async function gateCheck(filePath, type) {
  var prompt = '检查文件是否有效。文件路径: ' + filePath + '\n\n'
    + '1. 使用 Bash 执行 test -f 检查文件是否存在\n'
    + '2. 如果存在，检查文件大小 (wc -c)\n'
    + '3. 如果是 JSON 文件，用 node -e 验证 JSON.parse 格式\n\n'
    + '输出 JSON: {"exists": bool, "size": number, "valid": bool, "reason": "string"}'
  var result = await agent(prompt, {
    label: '门控检查:' + filePath.split('/').pop(),
  }).then(function(r) {
    return r || { exists: false, size: 0, valid: false, reason: 'agent 返回空' }
  }).catch(function(err) {
    return { exists: false, size: 0, valid: false, reason: err.message }
  })
  return result
}

/**
 * 门控检查：确保产物有效后才进入下一阶段
 * @param {string} filePath - 文件路径
 * @param {string} type - 文件类型
 * @param {string} stageName - 阶段名称（用于日志）
 * @returns {Promise<boolean>} - 是否通过门控
 */
async function gate(filePath, type, stageName) {
  log('门控检查: ' + stageName + ' → ' + filePath.split('/').pop())
  var check = await gateCheck(filePath, type)
  if (check.exists && check.valid) {
    log('✓ 门控通过: ' + stageName + '（' + check.size + ' bytes）')
    return true
  } else {
    log('✗ 门控失败: ' + stageName + ' — ' + check.reason)
    return false
  }
}

// 主工作流
var args = typeof globalThis.args !== 'undefined' ? globalThis.args : {}
var scope = (args && args.scope) || 'full'
var targetDir = (args && args.targetDir) || '02-dev/dev_12'
var dateStr = (args && args.date) || '2026-06-22'

log('=== 并行审计 v2（门控版）===')
log('模式: ' + scope)
log('目标: ' + targetDir)
log('日期: ' + dateStr)

// ═══════════════════════════════════════════════════════════════
// Phase 1: 预处理 — 创建目录 + 并行调查
// ═══════════════════════════════════════════════════════════════
phase('预处理')

var topicDir = 'docs/3.analysis/diagnosis/01-active/' + dateStr + '-代码审计-' + scope

// 创建目录结构
var dirsToCreate = [
  topicDir,
  topicDir + '/00-preprocessing',
  topicDir + '/01-scan-batch1',
  topicDir + '/02-scan-batch2',
  topicDir + '/03-scan-batch3',
  topicDir + '/04-review',
  topicDir + '/05-report',
]

await agent('执行 mkdir 命令创建以下目录（使用 Bash 工具，用 mkdir -p）：\n\n' + dirsToCreate.map(function(d) { return 'mkdir -p ' + d }).join('\n'), {
  label: '创建目录',
})
log('目录结构已创建: ' + topicDir)

// 并行预处理：3 个调查任务同时进行
log('启动并行预处理调查...')
await parallel([
  function() {
    return agent('你是文件统计专家。扫描 ' + targetDir + ' 目录，统计 TypeScript/JavaScript 文件：\n1. 用 Glob 查找 **/*.ts, **/*.tsx, **/*.js, **/*.jsx 文件\n2. 按目录统计文件数量\n3. 统计总行数\n4. 识别最大的 10 个文件\n\n使用 Bash 工具执行 shell 命令获取准确数据。\n\n将结果写入文件（使用 Write 工具）：' + topicDir + '/00-preprocessing/file-stats.json\n\nJSON 格式：{"totalFiles": 数字, "totalLines": 数字, "byDirectory": {"path": count}, "largestFiles": [{"file": "path", "lines": 数字}]}', {
      label: '预处理-文件统计',
    }).catch(function(err) {
      log('文件统计失败: ' + err.message)
      return null
    })
  },
  function() {
    return agent('你是模块结构分析专家。分析 ' + targetDir + ' 的模块结构：\n1. 用 Glob 找到所有 src/ 和 backend/src/ 下的目录\n2. 识别主要模块边界\n3. 分析模块间的 import 依赖关系\n4. 找出核心模块和工具模块\n\n将结果写入文件（使用 Write 工具）：' + topicDir + '/00-preprocessing/module-map.json', {
      label: '预处理-模块结构',
    }).catch(function(err) {
      log('模块结构分析失败: ' + err.message)
      return null
    })
  },
  function() {
    return agent('你是依赖分析专家。分析 ' + targetDir + ' 的项目依赖：\n1. 读取 package.json（根目录和 backend/）\n2. 分析 dependencies 和 devDependencies\n3. 检查版本过时、安全风险\n4. 检查 TypeScript 配置\n\n将结果写入文件（使用 Write 工具）：' + topicDir + '/00-preprocessing/dependency-analysis.json', {
      label: '预处理-依赖分析',
    }).catch(function(err) {
      log('依赖分析失败: ' + err.message)
      return null
    })
  },
])

// ═══════════════════════════════════════════════════════════════
// 门控：预处理
// ═══════════════════════════════════════════════════════════════
phase('门控-预处理')
var preprocessingFiles = [
  topicDir + '/00-preprocessing/file-stats.json',
  topicDir + '/00-preprocessing/module-map.json',
  topicDir + '/00-preprocessing/dependency-analysis.json',
]
var preprocessingPass = 0
for (var idx = 0; idx < preprocessingFiles.length; idx++) {
  if (await gate(preprocessingFiles[idx], 'json', '预处理')) preprocessingPass++
}
log('预处理门控: ' + preprocessingPass + '/' + preprocessingFiles.length + ' 通过')
if (preprocessingPass === 0) {
  log('警告：所有预处理文件缺失，继续执行但可能影响后续分析质量')
}

// ═══════════════════════════════════════════════════════════════
// 扫描维度函数
// ═══════════════════════════════════════════════════════════════
async function scanDimension(dim, targetDir) {
  return agent('你是' + dim.name + '审计专家。\n\n审计目标：' + targetDir + ' 目录下的 TypeScript/JavaScript 代码\n审计维度：' + dim.name + '\n' + dim.prompt + '\n\n要求：\n1. 使用 Glob 工具查找 ' + targetDir + ' 下的 .ts/.tsx/.js/.jsx 文件\n2. 使用 Read 工具读取可疑文件\n3. 每个发现包含 file、line、severity(P1-P4)、title、description、evidence\n4. evidence 字段必须填写：简要记录关键代码片段、grep 命令结果、或直接观察到的行为（如 "L42: const x = JSON.parse(raw) 无 try-catch"）\n5. 按 severity 从高到低排序\n6. 输出最多 15 个最重要的问题', {
    label: '扫描:' + dim.name,
    schema: FINDINGS_SCHEMA,
  }).then(function(result) {
    return {
      dimension: dim.key,
      dimensionName: dim.name,
      weight: dim.weight,
      findings: (result && result.findings) || [],
    }
  }).catch(function(err) {
    log('扫描 ' + dim.name + ' 失败: ' + err.message)
    return { dimension: dim.key, dimensionName: dim.name, weight: dim.weight, findings: [] }
  })
}

// ═══════════════════════════════════════════════════════════════
// Phase 2-4: 分批并行扫描 + 门控
// ═══════════════════════════════════════════════════════════════
var allFindings = []

// --- 批次 1 ---
phase('并行扫描-批次1')
var batch1 = [AUDIT_DIMENSIONS[0], AUDIT_DIMENSIONS[1], AUDIT_DIMENSIONS[2]]
var results1 = await parallel(batch1.map(function(dim) {
  return function() { return scanDimension(dim, targetDir) }
}))
var batch1Findings = []
results1.forEach(function(r) {
  r.findings.forEach(function(f) {
    var enriched = Object.assign({}, f, { dimension: r.dimension, dimensionName: r.dimensionName, weight: r.weight })
    allFindings.push(enriched)
    batch1Findings.push(enriched)
  })
})
await agent('使用 Write 工具将以下 JSON 写入 ' + topicDir + '/01-scan-batch1/findings.json：\n\n' + JSON.stringify({ batch: 1, dimensions: batch1.map(function(d) { return d.key }), count: batch1Findings.length, findings: batch1Findings }, null, 2), {
  label: '持久化-批次1',
})
log('批次1完成：' + batch1Findings.length + ' 个问题')

// 门控：批次1
phase('门控-批次1')
if (!await gate(topicDir + '/01-scan-batch1/findings.json', 'json', '批次1扫描')) {
  log('警告：批次1扫描文件缺失')
}

// --- 批次 2 ---
phase('并行扫描-批次2')
var batch2 = [AUDIT_DIMENSIONS[3], AUDIT_DIMENSIONS[4], AUDIT_DIMENSIONS[5]]
var results2 = await parallel(batch2.map(function(dim) {
  return function() { return scanDimension(dim, targetDir) }
}))
var batch2Findings = []
results2.forEach(function(r) {
  r.findings.forEach(function(f) {
    var enriched = Object.assign({}, f, { dimension: r.dimension, dimensionName: r.dimensionName, weight: r.weight })
    allFindings.push(enriched)
    batch2Findings.push(enriched)
  })
})
await agent('使用 Write 工具将以下 JSON 写入 ' + topicDir + '/02-scan-batch2/findings.json：\n\n' + JSON.stringify({ batch: 2, dimensions: batch2.map(function(d) { return d.key }), count: batch2Findings.length, findings: batch2Findings }, null, 2), {
  label: '持久化-批次2',
})
log('批次2完成：' + batch2Findings.length + ' 个问题')

// 门控：批次2
phase('门控-批次2')
if (!await gate(topicDir + '/02-scan-batch2/findings.json', 'json', '批次2扫描')) {
  log('警告：批次2扫描文件缺失')
}

// --- 批次 3 ---
phase('并行扫描-批次3')
var batch3 = [AUDIT_DIMENSIONS[6], AUDIT_DIMENSIONS[7], AUDIT_DIMENSIONS[8]]
var results3 = await parallel(batch3.map(function(dim) {
  return function() { return scanDimension(dim, targetDir) }
}))
var batch3Findings = []
results3.forEach(function(r) {
  r.findings.forEach(function(f) {
    var enriched = Object.assign({}, f, { dimension: r.dimension, dimensionName: r.dimensionName, weight: r.weight })
    allFindings.push(enriched)
    batch3Findings.push(enriched)
  })
})
await agent('使用 Write 工具将以下 JSON 写入 ' + topicDir + '/03-scan-batch3/findings.json：\n\n' + JSON.stringify({ batch: 3, dimensions: batch3.map(function(d) { return d.key }), count: batch3Findings.length, findings: batch3Findings }, null, 2), {
  label: '持久化-批次3',
})
log('批次3完成：' + batch3Findings.length + ' 个问题')

// 门控：批次3
phase('门控-批次3')
if (!await gate(topicDir + '/03-scan-batch3/findings.json', 'json', '批次3扫描')) {
  log('警告：批次3扫描文件缺失')
}

// ═══════════════════════════════════════════════════════════════
// Phase 5: 合并扫描结果
// ═══════════════════════════════════════════════════════════════
phase('合并扫描')
allFindings.sort(function(a, b) {
  var order = { P1: 0, P2: 1, P3: 2, P4: 3 }
  return (order[a.severity] || 4) - (order[b.severity] || 4)
})

var p1Count = allFindings.filter(function(f) { return f.severity === 'P1' }).length
var p2Count = allFindings.filter(function(f) { return f.severity === 'P2' }).length
var p3Count = allFindings.filter(function(f) { return f.severity === 'P3' }).length
var p4Count = allFindings.filter(function(f) { return f.severity === 'P4' }).length

await agent('使用 Write 工具将以下 JSON 写入 ' + topicDir + '/scan-findings.json：\n\n' + JSON.stringify({
  metadata: { version: 'v2', scope: scope, targetDir: targetDir, date: dateStr },
  summary: { totalIssues: allFindings.length, p1: p1Count, p2: p2Count, p3: p3Count, p4: p4Count },
  findings: allFindings,
}, null, 2), {
  label: '持久化-合并扫描',
})
log('合并扫描完成：共 ' + allFindings.length + ' 个问题 (P1:' + p1Count + ' P2:' + p2Count + ' P3:' + p3Count + ' P4:' + p4Count + ')')

// 门控：合并
phase('门控-合并')
if (!await gate(topicDir + '/scan-findings.json', 'json', '合并扫描')) {
  log('错误：合并扫描文件缺失，无法继续')
  return { error: '合并扫描文件缺失', topicDir: topicDir }
}

// ═══════════════════════════════════════════════════════════════
// Phase 6: 并行复核（并发3 + 超时保护 + 降级）
// ═══════════════════════════════════════════════════════════════
phase('并行复核')
var findingsToReview = allFindings
var allVerdicts = []
var REVIEW_BATCH_SIZE = 3  // 并发3，低于 API 限制 4
var REVIEW_TIMEOUT = 600000  // 5分钟超时

if (findingsToReview.length > 0) {
  log('全量复核 ' + findingsToReview.length + ' 个问题（并发' + REVIEW_BATCH_SIZE + '）...')
  var totalBatches = Math.ceil(findingsToReview.length / REVIEW_BATCH_SIZE)

  for (var i = 0; i < findingsToReview.length; i += REVIEW_BATCH_SIZE) {
    var batch = findingsToReview.slice(i, i + REVIEW_BATCH_SIZE)
    var batchIdx = Math.floor(i / REVIEW_BATCH_SIZE) + 1
    log('复核批次 ' + batchIdx + '/' + totalBatches + '（' + batch.length + ' 个）')

    // 并发3 + 超时保护
    var batchVerdicts = await Promise.race([
      parallel(batch.map(function(finding) {
        return function() {
          return agent('验证审计发现：文件 ' + finding.file + ' 行 ' + (finding.line || 'N/A') + '\n问题: ' + finding.title + '\n严重级别: ' + finding.severity + '\n原始证据: ' + (finding.evidence || '未提供') + '\n\n读取文件验证：\n- 如确认真实：evidence 写验证依据（如"确认 L42 存在未保护的 JSON.parse"）\n- 如是误报：evidence 写反证（如"该函数已被上游 try-catch 包裹"、"line 42 实际是注释"）\nevidence 不能为空。', {
            label: '复核:' + finding.title.slice(0, 20),
            schema: VERDICT_SCHEMA,
          }).then(function(v) {
            return { findingId: finding.file + ':' + finding.line, finding: finding, verdict: v || { isConfirmed: true, isFalsePositive: false } }
          }).catch(function() {
            return { findingId: finding.file + ':' + finding.line, finding: finding, verdict: { isConfirmed: true, isFalsePositive: false } }
          })
        }
      })),
      new Promise(function(resolve) {
        setTimeout(function() {
          log('复核批次 ' + batchIdx + ' 超时，跳过')
          resolve(batch.map(function(finding) {
            return { findingId: finding.file + ':' + finding.line, finding: finding, verdict: { isConfirmed: true, isFalsePositive: false, reviewNote: '超时跳过' } }
          }))
        }, REVIEW_TIMEOUT)
      })
    ])

    allVerdicts = allVerdicts.concat(batchVerdicts)

    // 每批复核完成后持久化
    await agent('使用 Write 工具将以下 JSON 写入 ' + topicDir + '/04-review/verdicts-batch' + batchIdx + '.json：\n\n' + JSON.stringify({
      batch: batchIdx,
      totalBatches: totalBatches,
      count: batchVerdicts.length,
      verdicts: batchVerdicts.map(function(v) {
        return { findingId: v.findingId, file: v.finding.file, title: v.finding.title, severity: v.finding.severity, isConfirmed: v.verdict.isConfirmed, isFalsePositive: v.verdict.isFalsePositive, confidence: v.verdict.confidence, reviewNote: v.verdict.reviewNote, evidence: v.verdict.evidence || '' }
      }),
    }, null, 2), {
      label: '持久化-复核批次' + batchIdx,
    })
    log('复核批次 ' + batchIdx + ' 完成，已持久化')
  }

  var confirmed = allVerdicts.filter(function(r) { return r.verdict.isConfirmed && !r.verdict.isFalsePositive })
  var falsePositives = allVerdicts.filter(function(r) { return r.verdict.isFalsePositive })

  // 合并复核结果
  var falsePositiveKeys = {}
  falsePositives.forEach(function(r) { falsePositiveKeys[r.findingId] = true })
  var finalFindings = allFindings.filter(function(f) { return !falsePositiveKeys[f.file + ':' + f.line] })

  await agent('使用 Write 工具将以下 JSON 写入 ' + topicDir + '/04-review/verdicts.json：\n\n' + JSON.stringify({
    totalReviewed: allVerdicts.length,
    confirmed: confirmed.length,
    falsePositives: falsePositives.length,
    finalFindingsCount: finalFindings.length,
    verdicts: allVerdicts.map(function(v) {
      return { findingId: v.findingId, file: v.finding.file, severity: v.finding.severity, isConfirmed: v.verdict.isConfirmed, isFalsePositive: v.verdict.isFalsePositive, evidence: v.verdict.evidence || '' }
    }),
  }, null, 2), {
    label: '持久化-复核汇总',
  })
  log('复核完成：确认 ' + confirmed.length + '，误报 ' + falsePositives.length + '，最终 ' + finalFindings.length + ' 个问题')
} else {
  log('无问题需要复核')
  var finalFindings = allFindings
}

// 门控：复核
phase('门控-复核')
if (!await gate(topicDir + '/04-review/verdicts.json', 'json', '复核汇总')) {
  log('警告：复核汇总文件缺失，将使用原始扫描结果')
  var finalFindings = allFindings  // 降级：跳过复核过滤
}

// ═══════════════════════════════════════════════════════════════
// Phase 7: 报告生成
// ═══════════════════════════════════════════════════════════════
phase('报告生成')
var reportResult = await agent('生成审计报告。\n\n审计目标：' + targetDir + '\n审计日期：' + dateStr + '\n\n最终发现（共 ' + finalFindings.length + ' 个，已过滤误报）：\n' + JSON.stringify(finalFindings.slice(0, 60), null, 2) + '\n\n维度权重：security 20%, stability 15%, architecture 15%, quality 15%, performance 10%, testability 10%, observability 5%, maintainability 5%, compatibility 5%\n\n请生成：\n1. 总体评分(0-10)\n2. 各维度评分\n3. 问题汇总（按维度分组，每个问题必须包含 evidence 字段作为"证据"栏）\n4. 优先修复路线图\n5. 亮点与优势\n\n报告格式要求：\n- 每个问题的 Markdown 格式为：标题 + 文件/行号 + 问题描述 + **证据** + 风险 + 建议\n- 证据栏直接引用 findings 中的 evidence 字段内容（代码片段、grep 结果等）\n- 证据可以简略，但不能为空\n\n输出纯 Markdown 格式报告。', {
  label: '生成报告',
  schema: {
    type: 'object',
    properties: {
      summary: { type: 'object', properties: { overallScore: { type: 'number' }, totalIssues: { type: 'number' } } },
      dimensionScores: { type: 'array', items: { type: 'object', properties: { dimension: { type: 'string' }, score: { type: 'number' }, issues: { type: 'number' } } } },
      reportMarkdown: { type: 'string' },
    },
    required: ['summary', 'reportMarkdown'],
  },
})

var overallScore = (reportResult && reportResult.summary && reportResult.summary.overallScore) || 'N/A'
var reportMarkdown = (reportResult && reportResult.reportMarkdown) || '# 审计报告生成失败'

// 草稿报告持久化
await agent('使用 Write 工具将以下内容写入 ' + topicDir + '/05-report/report-draft.md：\n\n' + reportMarkdown, {
  label: '持久化-报告草稿',
})
log('报告生成完成：评分 ' + overallScore + '/10')

// 门控：报告
phase('门控-报告')
if (!await gate(topicDir + '/05-report/report-draft.md', 'markdown', '报告草稿')) {
  log('错误：报告草稿缺失，无法继续')
  return { error: '报告草稿缺失', topicDir: topicDir }
}

// ═══════════════════════════════════════════════════════════════
// Phase 8: 最终落盘
// ═══════════════════════════════════════════════════════════════
phase('最终落盘')

var mainReportContent = '---\n'
mainReportContent += 'description: 代码审计报告 v2（9维度并行审计）— ' + targetDir + '\n'
mainReportContent += 'date: ' + dateStr + '\n'
mainReportContent += 'scope: ' + scope + '\n'
mainReportContent += 'version: v2\n'
mainReportContent += 'overall_score: ' + overallScore + '\n'
mainReportContent += 'total_issues: ' + finalFindings.length + '\n'
mainReportContent += 'status: active\n'
mainReportContent += 'tags: [audit, code-quality, security, architecture]\n'
mainReportContent += '---\n\n'
mainReportContent += reportMarkdown

var findingsData = {
  metadata: { version: 'v2', scope: scope, targetDir: targetDir, date: dateStr },
  summary: {
    totalIssues: finalFindings.length,
    p1Count: p1Count,
    p2Count: p2Count,
    p3Count: p3Count,
    p4Count: p4Count,
    overallScore: overallScore,
  },
  findings: finalFindings,
}

var runResult = {
  workflow: 'audit-parallel-v2',
  status: 'completed',
  auditResult: p1Count > 0 ? 'blocked' : 'pass',
  date: dateStr,
  scope: scope,
  targetDir: targetDir,
  overallScore: overallScore,
  totalFindings: finalFindings.length,
  findingsBySeverity: { P1: p1Count, P2: p2Count, P3: p3Count, P4: p4Count },
  products: {
    mainReport: topicDir + '/README.md',
    findingsData: topicDir + '/audit-findings.json',
    runResult: topicDir + '/run-result.json',
    scanFindings: topicDir + '/scan-findings.json',
    preprocessing: {
      fileStats: topicDir + '/00-preprocessing/file-stats.json',
      moduleMap: topicDir + '/00-preprocessing/module-map.json',
      dependencyAnalysis: topicDir + '/00-preprocessing/dependency-analysis.json',
    },
    scanBatches: [
      topicDir + '/01-scan-batch1/findings.json',
      topicDir + '/02-scan-batch2/findings.json',
      topicDir + '/03-scan-batch3/findings.json',
    ],
    reviewVerdicts: topicDir + '/04-review/verdicts.json',
    reportDraft: topicDir + '/05-report/report-draft.md',
  },
}

await agent('使用 Write 工具依次写入以下 3 个文件：\n\n1. ' + topicDir + '/README.md\n内容：\n```\n' + mainReportContent + '\n```\n\n2. ' + topicDir + '/audit-findings.json\n内容：\n```json\n' + JSON.stringify(findingsData, null, 2) + '\n```\n\n3. ' + topicDir + '/run-result.json\n内容：\n```json\n' + JSON.stringify(runResult, null, 2) + '\n```', {
  label: '最终落盘',
})

log('最终落盘完成：' + topicDir)

// ═══════════════════════════════════════════════════════════════
// 门控：最终验证
// ═══════════════════════════════════════════════════════════════
phase('门控-完成')
var finalFiles = [
  topicDir + '/README.md',
  topicDir + '/audit-findings.json',
  topicDir + '/run-result.json',
]
var finalPass = 0
for (var fi = 0; fi < finalFiles.length; fi++) {
  if (await gate(finalFiles[fi], 'any', '最终产物')) finalPass++
}
log('最终门控: ' + finalPass + '/' + finalFiles.length + ' 通过')

// ═══════════════════════════════════════════════════════════════
// 返回结果
// ═══════════════════════════════════════════════════════════════
return {
  version: 'v2-gated',
  scope: scope,
  date: dateStr,
  topicDir: topicDir,
  totalFindings: finalFindings.length,
  findingsBySeverity: runResult.findingsBySeverity,
  overallScore: overallScore,
  products: runResult.products,
  gateResults: {
    preprocessing: preprocessingPass + '/' + preprocessingFiles.length,
    final: finalPass + '/' + finalFiles.length,
  },
  findings: finalFindings,
}
