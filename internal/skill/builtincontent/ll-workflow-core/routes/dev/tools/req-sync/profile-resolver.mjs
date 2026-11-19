#!/usr/bin/env node
/**
 * profile-resolver.mjs — 目录 Profile 与 .ll-workflow.yaml 解析的专用 SSOT 数据源
 *
 * 设计目标：
 *   - 作为所有 req-sync / generate-docs-index / sync 脚本的**单一数据源**
 *   - 实现 SKILL.md §"项目级配置" 声明的 5 层优先级链
 *   - 零外部依赖（仅 node:fs + node:path + node:process），便于在任何 Node 环境运行
 *
 * 数据源（按优先级从低到高合并）：
 *   1. global_default       config/directory-layout.yaml#profiles/default
 *   2. registry_default     config/route-registry.yaml#registry.defaults.directory_profile_ref
 *   3. route_binding        config/route-registry.yaml#routes[?].directory_profile_ref
 *   4. project_config_override  <workspaceRoot>/.ll-workflow.yaml#slots
 *   5. workflow_override    运行时显式传入的参数（CLI flag / 函数参数）
 *
 * 用法（作为模块）：
 *   import { resolveProfile } from "./profile-resolver.mjs";
 *   const ctx = resolveProfile({
 *     workspaceRoot: "/path/to/project",
 *     targetDevDir: "/path/to/project/02-dev/dev_07",
 *     profileOverride: "generic_docs_v1",
 *   });
 *   ctx.resolveSemantic("done"); // → "/path/to/project/02-dev/dev_07/docs/tasks/done"
 *   ctx.resolveSlot("docs_root"); // → "/path/to/project/02-dev/dev_07/docs"
 *
 * 用法（独立运行）：
 *   node profile-resolver.mjs --workspace-root . --target-dev-dir ./02-dev/dev_07 --print-json
 *   node profile-resolver.mjs --workspace-root . --print-semantic done
 */

import { readFileSync, existsSync, statSync } from "node:fs";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

// ============================================================
// 路径常量
// ============================================================

const __filename = fileURLToPath(import.meta.url);
// routes/dev/tools/req-sync/profile-resolver.mjs → 项目根 = ../../../../
const SKILL_ROOT = resolve(dirname(__filename), "../../../..");
const CONFIG_DIR = join(SKILL_ROOT, "config");
const DIRECTORY_LAYOUT_PATH = join(CONFIG_DIR, "directory-layout.yaml");
const ROUTE_REGISTRY_PATH = join(CONFIG_DIR, "route-registry.yaml");
const PROJECT_CONFIG_FILENAME = ".ll-workflow.yaml";

// ============================================================
// 最小 YAML 解析器（仅支持本项目实际用法）
// ============================================================
//
// 覆盖以下场景：
//   - 顶层与嵌套 mapping（key: value）
//   - 字符串 value 带或不带引号
//   - inline list: [a, b, c]
//   - block list: - item
//   - 注释行 (# 或 //)
//   - 多行字符串 (>- 或 |- 折叠 / 字面)
//   - extends: profile_name 引用合并
//
// 不支持（项目中也未使用）：锚点 (&/*)、tag、流式 { }、[ ] 嵌套对象

function stripQuotes(s) {
  if (s == null) return s;
  s = String(s).trim();
  if (
    (s.startsWith('"') && s.endsWith('"')) ||
    (s.startsWith("'") && s.endsWith("'"))
  ) {
    return s.slice(1, -1);
  }
  return s;
}

function parseYaml(text) {
  const rawLines = text.split(/\r?\n/);
  const lines = rawLines.map((l) => l.replace(/\r$/, ""));
  const root = {};
  // stack: [{indent, container, isList}]
  const stack = [{ indent: -1, container: root, isList: false }];
  let i = 0;

  while (i < lines.length) {
    const rawLine = lines[i];
    const stripped = rawLine.replace(/#.*$/, "").replace(/\/\/.*$/, "").trimEnd();
    if (!stripped.trim()) {
      i++;
      continue;
    }

    const indent = rawLine.length - rawLine.trimStart().length;
    const trimmed = stripped.trim();

    // 弹出栈直到找到比自己 indent 小的层级
    while (stack.length > 1 && stack[stack.length - 1].indent > indent) {
      stack.pop();
    }
    const top = stack[stack.length - 1];

    // block list item: "- xxx"
    if (trimmed.startsWith("- ")) {
      const itemText = trimmed.slice(2).trim();
      if (!top.isList) {
        // 父容器不是 list，跳过（理论上 schema 不会这么写）
        i++;
        continue;
      }
      if (itemText.includes(":")) {
        // list 内的 mapping item：- key: value
        const colonIdx = itemText.indexOf(":");
        const key = itemText.slice(0, colonIdx).trim();
        const val = itemText.slice(colonIdx + 1).trim();
        const obj = {};
        if (val === "") {
          obj[key] = {};
          top.container.push(obj);
          stack.push({ indent: indent + 2, container: obj[key], isList: false });
        } else if (val.startsWith("[") && val.endsWith("]")) {
          obj[key] = parseInlineList(val);
          top.container.push(obj);
        } else {
          obj[key] = stripQuotes(val);
          top.container.push(obj);
          // 续字段：list item 内的多行 mapping（缩进 > 当前 item，不以 - 开头且含 :）
          let j = i + 1;
          while (j < lines.length) {
            const lr2 = lines[j];
            if (!lr2.trim()) { j++; continue; }
            const li2 = lr2.length - lr2.trimStart().length;
            if (li2 < indent + 2) break;
            const lt2 = lr2.replace(/#.*$/, "").trimEnd().trim();
            if (lt2.startsWith("- ")) break;
            if (!lt2.includes(":")) break;
            const ci2 = lt2.indexOf(":");
            const k2 = lt2.slice(0, ci2).trim();
            const v2 = lt2.slice(ci2 + 1).trim();
            obj[k2] = stripQuotes(v2);
            j++;
          }
          i = j - 1;
        }
      } else if (itemText) {
        top.container.push(stripQuotes(itemText));
      }
      i++;
      continue;
    }

    // inline list in value: key: [a, b, c]
    if (trimmed.includes(":")) {
      const colonIdx = trimmed.indexOf(":");
      const key = trimmed.slice(0, colonIdx).trim();
      const val = trimmed.slice(colonIdx + 1).trim();

      if (!top.isList && typeof top.container === "object") {
        if (val === "" || val === ">" || val === ">" || val === "|") {
          // 多行字符串标记
          if (val === "|" || val === ">" || val.startsWith(">") || val.startsWith("|")) {
            const folded = val.startsWith(">");
            const block = [];
            i++;
            while (i < lines.length) {
              const nextRaw = lines[i];
              const nextIndent = nextRaw.length - nextRaw.trimStart().length;
              if (nextIndent <= indent) break;
              if (nextRaw.trim()) {
                block.push(nextRaw.slice(indent + 2).replace(/\s+$/, ""));
              }
              i++;
            }
            top.container[key] = folded ? block.join(" ") : block.join("\n");
            continue;
          }
          // 嵌套 mapping / list
          // 判断下一行的开头
          const nextRaw = lines[i + 1];
          if (nextRaw && nextRaw.trim()) {
            const nextIndent = nextRaw.length - nextRaw.trimStart().length;
            if (nextIndent > indent) {
              const nextTrimmed = nextRaw.trim();
              if (nextTrimmed.startsWith("- ")) {
                top.container[key] = [];
                stack.push({ indent: nextIndent, container: top.container[key], isList: true });
              } else {
                top.container[key] = {};
                stack.push({ indent: nextIndent, container: top.container[key], isList: false });
              }
            } else {
              top.container[key] = null;
            }
          } else {
            top.container[key] = null;
          }
        } else if (val.startsWith("[") && val.endsWith("]")) {
          top.container[key] = parseInlineList(val);
        } else {
          top.container[key] = stripQuotes(val);
        }
      }
    }
    i++;
  }

  return root;
}

function parseInlineList(s) {
  const inner = s.slice(1, -1).trim();
  if (!inner) return [];
  return inner
    .split(",")
    .map((x) => stripQuotes(x.trim()))
    .filter((x) => x !== "");
}

// ============================================================
// 文件加载
// ============================================================

function readYamlSafe(path) {
  if (!existsSync(path)) return null;
  try {
    return parseYaml(readFileSync(path, "utf-8"));
  } catch (e) {
    throw new Error(`YAML 解析失败: ${path} → ${e.message}`);
  }
}

function findWorkspaceRoot(startDir) {
  let dir = resolve(startDir);
  for (let i = 0; i < 10; i++) {
    if (existsSync(join(dir, PROJECT_CONFIG_FILENAME))) return dir;
    if (existsSync(join(dir, ".git"))) return dir;
    const parent = dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return null;
}

// ============================================================
// 变量替换（${var | default(x)}）
// ============================================================

function substitute(template, vars) {
  if (typeof template !== "string") return template;
  return template.replace(/\$\{\s*([^}]+)\s*\}/g, (_, expr) => {
    const parts = expr.split("|").map((p) => p.trim());
    const name = parts[0];
    const defaultPart = parts.find((p) => p.startsWith("default("));
    let value = vars[name];
    if (value == null && defaultPart) {
      value = defaultPart.slice(8, -1).trim();
    }
    if (value == null) return "";
    // 支持简单拼接：workspaceRoot + '/worktrees'
    if (value.includes("+")) {
      const tokens = value.split("+").map((t) => t.trim().replace(/^['"]|['"]$/g, ""));
      return tokens
        .map((t) => (t in vars ? vars[t] : t))
        .join("");
    }
    return value;
  });
}

// ============================================================
// Profile 合并
// ============================================================

function deepMerge(base, override) {
  if (override == null) return base;
  if (typeof base !== "object" || Array.isArray(base)) return override;
  const out = { ...base };
  for (const [k, v] of Object.entries(override)) {
    if (v && typeof v === "object" && !Array.isArray(v) && typeof out[k] === "object" && !Array.isArray(out[k])) {
      out[k] = deepMerge(out[k], v);
    } else if (v !== undefined && v !== null) {
      out[k] = v;
    }
  }
  return out;
}

function resolveProfileRef(layout, ref) {
  // 形如 "./directory-layout.yaml#profiles/default"
  if (!ref || !ref.includes("#")) return null;
  const [pathPart, anchorPart] = ref.split("#");
  const segs = anchorPart.split("/").filter(Boolean);
  // 仅支持 profiles/{name} 形式（本项目唯一用法）
  let cur = layout;
  for (const seg of segs) {
    if (cur == null) return null;
    cur = cur[seg];
  }
  return cur;
}

// ============================================================
// Schema 校验 + Aliases 解析
// ============================================================
//
// validateProjectConfig —— 对 .ll-workflow.yaml 做轻量 schema 校验：
//   - mode: "strict" | "warn" | "off"
//   - strict: 第一个错误就抛错
//   - warn:   收集所有错误，返回 { valid: false, errors: [...] }
//   - off:    跳过校验，返回 { valid: true, errors: [] }
//
// resolveCustomAliases —— 把 aliases: [{name, maps_to}] 按 maps_to 聚合：
//   { route_short_name: [alias1, alias2, ...] }
//   供 init workflow 把项目自定义别名注入 intent_hints。

const VALID_ROUTES = [
  "init", "req", "dev", "aud", "fix",
  "branch", "merge", "hchk", "haud", "df", "gt",
];

const VALID_SLOT_KEYS = [
  "worktree_root", "worktree_slots_json",
  "docs_root",
  "task_inactive_root", "task_todo_root", "task_todo_version_root",
  "task_done_root", "task_done_version_root",
  "task_paused_root", "task_cancelled_root",
  "task_requirements_index", "task_readme_index",
  "docs_index", "docs_readme",
  "changelog_root", "weekly_root", "test_root",
  "tech_api_root", "tech_architecture_root", "tech_database_root",
  "tech_development_root", "tech_guide_root", "tech_specification_root",
  "tech_user_guide_root", "guide_root",
  "analysis_diagnosis_root", "analysis_diagnosis_active",
  "analysis_diagnosis_outdated", "analysis_report_root", "analysis_evidence_root",
  "governance_handoff_root", "governance_json_evidence_root",
  "governance_learning_backflow_root",
];

const VALID_SEMANTIC_KEYS = [
  "todo", "inactive_requirement", "done", "paused", "cancelled",
  "docs_index", "changelog",
];

const VALID_WORKFLOW_KEYS = [
  "init", "req", "dev", "aud", "fix",
  "branch", "merge", "hchk", "haud", "df", "gt",
];

const VALID_BASE_PROFILES = ["default", "generic_docs_v1", "minimal_workspace", "numbered_docs_v1"];

export function validateProjectConfig(cfg, options = {}) {
  const mode = options.mode || "strict";
  if (mode === "off") return { valid: true, errors: [], mode };

  const errors = [];
  if (!cfg || typeof cfg !== "object") {
    const e = new Error("project config is not an object");
    if (mode === "strict") throw e;
    return { valid: false, errors: [e.message], mode };
  }

  // version 必填
  if (!cfg.version || typeof cfg.version !== "string") {
    errors.push("missing required field: version (string)");
  }

  // base_profile 取值合法
  if (cfg.base_profile != null && !VALID_BASE_PROFILES.includes(cfg.base_profile)) {
    errors.push(
      `base_profile: invalid value "${cfg.base_profile}", ` +
      `allowed: ${VALID_BASE_PROFILES.join(", ")}`
    );
  }

  // project.name 必填（如有 project 段）
  if (cfg.project != null) {
    if (typeof cfg.project !== "object") {
      errors.push("project: must be an object");
    } else if (!cfg.project.name || typeof cfg.project.name !== "string") {
      errors.push("project.name: required string");
    }
  }

  // slots 仅允许已知 key
  if (cfg.slots != null) {
    if (typeof cfg.slots !== "object" || Array.isArray(cfg.slots)) {
      errors.push("slots: must be an object");
    } else {
      for (const k of Object.keys(cfg.slots)) {
        if (!VALID_SLOT_KEYS.includes(k)) {
          errors.push(`slots.${k}: unknown slot key`);
        }
      }
    }
  }

  // semantics 仅允许已知 key
  if (cfg.semantics != null) {
    if (typeof cfg.semantics !== "object" || Array.isArray(cfg.semantics)) {
      errors.push("semantics: must be an object");
    } else {
      for (const k of Object.keys(cfg.semantics)) {
        if (!VALID_SEMANTIC_KEYS.includes(k)) {
          errors.push(`semantics.${k}: unknown semantic key`);
        }
      }
    }
  }

  // workflows 仅允许已知 route 名
  if (cfg.workflows != null) {
    if (typeof cfg.workflows !== "object" || Array.isArray(cfg.workflows)) {
      errors.push("workflows: must be an object");
    } else {
      for (const k of Object.keys(cfg.workflows)) {
        if (!VALID_WORKFLOW_KEYS.includes(k)) {
          errors.push(`workflows.${k}: unknown workflow key`);
        }
      }
    }
  }

  // aliases 必须是 { name, maps_to }[]
  if (cfg.aliases != null) {
    if (!Array.isArray(cfg.aliases)) {
      errors.push("aliases: must be an array of { name, maps_to }");
    } else {
      cfg.aliases.forEach((a, i) => {
        if (!a || typeof a !== "object") {
          errors.push(`aliases[${i}]: must be an object`);
          return;
        }
        if (!a.name || typeof a.name !== "string") {
          errors.push(`aliases[${i}].name: required string`);
        }
        if (!a.maps_to || typeof a.maps_to !== "string") {
          errors.push(`aliases[${i}].maps_to: required string`);
        } else if (!VALID_ROUTES.includes(a.maps_to)) {
          errors.push(
            `aliases[${i}].maps_to: "${a.maps_to}" is not a known route ` +
            `(allowed: ${VALID_ROUTES.join(", ")})`
          );
        }
      });
    }
  }

  if (errors.length > 0) {
    if (mode === "strict") {
      const e = new Error(`project config validation failed:\n  - ${errors.join("\n  - ")}`);
      e.errors = errors;
      throw e;
    }
    return { valid: false, errors, mode };
  }
  return { valid: true, errors: [], mode };
}

export function resolveCustomAliases(cfg) {
  if (!cfg || !Array.isArray(cfg.aliases)) return {};
  const out = {};
  for (const a of cfg.aliases) {
    if (!a || typeof a !== "object") continue;
    if (!a.maps_to || typeof a.maps_to !== "string") continue;
    if (!a.name || typeof a.name !== "string") continue;
    if (!out[a.maps_to]) out[a.maps_to] = [];
    out[a.maps_to].push(a.name);
  }
  return out;
}

// ============================================================
// 核心 API：resolveProfile
// ============================================================

/**
 * @param {Object} options
 * @param {string} [options.workspaceRoot]  项目根路径；缺省时自动向上查找
 * @param {string} [options.targetDevDir]   dev_xx 目录绝对路径
 * @param {string} [options.version]        版本号（如 "07"），缺省从 targetDevDir 推断
 * @param {string} [options.worktree]       worktree 标识
 * @param {string} [options.profileOverride] 显式覆盖 profile 名
 * @param {string} [options.routeId]        当前 route id（如 route.health-check）
 * @returns {{
 *   workspaceRoot: string,
 *   targetDevDir: string,
 *   version: string,
 *   slots: Object<string, string>,
 *   semantics: Object<string, string>,
 *   resolveSlot: (name: string) => string,
 *   resolveSemantic: (name: string) => string,
 *   resolveAll: () => Object<string, string>,
 * }}
 */
export function resolveProfile(options = {}) {
  const workspaceRoot =
    options.workspaceRoot ||
    findWorkspaceRoot(process.cwd()) ||
    process.cwd();

  const targetDevDir = options.targetDevDir || join(workspaceRoot, "docs");
  const version = options.version || inferVersion(targetDevDir) || "00";
  const worktree = options.worktree || ".";

  // 1) global_default: directory-layout.yaml
  const layout = readYamlSafe(DIRECTORY_LAYOUT_PATH);
  if (!layout) {
    throw new Error(`directory-layout.yaml 未找到: ${DIRECTORY_LAYOUT_PATH}`);
  }


  // 2) registry_default + 3) route_binding
  const registry = readYamlSafe(ROUTE_REGISTRY_PATH);
  let profileRef = layout.profiles?.default ? "./directory-layout.yaml#profiles/default" : null;
  if (registry?.registry?.defaults?.directory_profile_ref) {
    profileRef = registry.registry.defaults.directory_profile_ref;
  }
  if (options.routeId && registry?.routes) {
    const route = registry.routes.find((r) => r.id === options.routeId);
    if (route?.directory_profile_ref) {
      profileRef = route.directory_profile_ref;
    }
  }

  // profile 选择
  let profileName = options.profileOverride || extractProfileName(profileRef);
  let profile = layout.profiles?.[profileName];
  if (!profile) {
    throw new Error(`profile 不存在: ${profileName}`);
  }
  // 处理 extends
  if (profile.extends) {
    const base = layout.profiles?.[profile.extends];
    if (base) profile = deepMerge(base, profile);
  }

  // 4) project_config_override: .ll-workflow.yaml
  const projectCfgPath = join(workspaceRoot, PROJECT_CONFIG_FILENAME);
  const projectCfg = readYamlSafe(projectCfgPath);
  if (projectCfg) {
    // 校验（strict | warn | off）
    const validateMode = options.validate || "strict";
    const validation = validateProjectConfig(projectCfg, { mode: validateMode });
    if (validateMode !== "off" && !validation.valid) {
      // warn 模式下不阻断，但要 console.error 让维护者看到
      console.error(
        `[profile-resolver] project config validation warnings (${'$'}{validation.errors.length}):\n` +
        validation.errors.map((e) => `  - ${'$'}{e}`).join("\n")
      );
    }
    if (projectCfg.slots) {
      profile.slots = { ...(profile.slots || {}), ...projectCfg.slots };
    }
    if (projectCfg.base_profile && !options.profileOverride) {
      const overrideProfile = layout.profiles?.[projectCfg.base_profile];
      if (overrideProfile) {
        const merged = deepMerge(overrideProfile, profile);
        profile = merged;
      }
    }
  }

  // 5) workflow_override: 调用方传入的 inline overrides
  if (options.slotOverrides) {
    profile.slots = { ...(profile.slots || {}), ...options.slotOverrides };
  }

  // 变量注入
  const vars = {
    workspaceRoot,
    targetDevDir,
    version,
    worktree,
    docs: projectCfg?.docs || "docs",
    worktree_root:
      options.worktreeRoot || join(workspaceRoot, "worktrees"),
  };

  // 解析所有 slot 的最终路径
  const resolvedSlots = {};
  for (const [name, tmpl] of Object.entries(profile.slots || {})) {
    resolvedSlots[name] = substitute(tmpl, vars);
  }

  // semantics 映射
  const semantics = profile.semantics || {};
  const resolveSlot = (name) => resolvedSlots[name] || name;
  const resolveSemantic = (name) => {
    const slotName = semantics[name];
    if (!slotName) return name;
    return resolvedSlots[slotName] || name;
  };
  const resolveAll = () => ({ ...resolvedSlots });

  return {
    workspaceRoot,
    targetDevDir,
    version,
    slots: resolvedSlots,
    semantics,
    resolveSlot,
    resolveSemantic,
    resolveAll,
    // 项目自定义别名（aliases 聚合）— 供 init workflow 注入 intent_hints
    customAliases: projectCfg ? resolveCustomAliases(projectCfg) : {},
    // 原始 project config 全文（供消费方按需读取）
    projectConfig: projectCfg || null,
    // 调试用
    _raw: profile,
    _projectConfigPath: projectCfgPath,
    _directoryLayoutPath: DIRECTORY_LAYOUT_PATH,
    _routeRegistryPath: ROUTE_REGISTRY_PATH,
  };
}

function extractProfileName(ref) {
  if (!ref) return "default";
  const m = ref.match(/profiles\/(\w+)/);
  return m ? m[1] : "default";
}

function inferVersion(targetDevDir) {
  const m = targetDevDir.match(/dev[_-]?(\d+)/i);
  return m ? m[1].padStart(2, "0") : null;
}

// ============================================================
// CLI 入口
// ============================================================

function parseCliArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--workspace-root") out.workspaceRoot = argv[++i];
    else if (a === "--target-dev-dir") out.targetDevDir = argv[++i];
    else if (a === "--version") out.version = argv[++i];
    else if (a === "--profile") out.profileOverride = argv[++i];
    else if (a === "--route-id") out.routeId = argv[++i];
    else if (a === "--worktree") out.worktree = argv[++i];
    else if (a === "--print-json") out.printJson = true;
    else if (a === "--print-semantic") out.printSemantic = argv[++i];
    else if (a === "--print-slot") out.printSlot = argv[++i];
    else if (a === "--help" || a === "-h") out.help = true;
  }
  return out;
}

function showHelp() {
  console.log(`profile-resolver — 目录 Profile 与 .ll-workflow.yaml 解析的 SSOT 数据源

用法：
  node profile-resolver.mjs [options]

选项：
  --workspace-root <path>   项目根目录（缺省自动向上查找）
  --target-dev-dir <path>   dev_xx 目录绝对路径
  --version <NN>            版本号（如 07）
  --worktree <id>           worktree 标识
  --profile <name>          显式覆盖 profile（如 generic_docs_v1）
  --route-id <id>           当前 route id（如 route.health-check）
  --print-json              输出完整解析结果为 JSON
  --print-semantic <name>   输出单个 semantic 解析后的路径
  --print-slot <name>       输出单个 slot 解析后的路径
  --help, -h                显示此帮助

示例：
  node profile-resolver.mjs --workspace-root . --print-json
  node profile-resolver.mjs --workspace-root . --print-semantic done
  node profile-resolver.mjs --workspace-root . --profile generic_docs_v1 --print-slot task_done_version_root
`);
}

// 仅当直接运行本文件时执行 CLI
if (import.meta.url === `file://${'$'}{process.argv[1]}`) {
  const args = parseCliArgs(process.argv.slice(2));
  if (args.help || Object.keys(args).length === 0) {
    showHelp();
    process.exit(args.help ? 0 : 1);
  }

  try {
    const ctx = resolveProfile({
      workspaceRoot: args.workspaceRoot,
      targetDevDir: args.targetDevDir,
      version: args.version,
      worktree: args.worktree,
      profileOverride: args.profileOverride,
      routeId: args.routeId,
    });

    if (args.printSemantic) {
      console.log(ctx.resolveSemantic(args.printSemantic));
    } else if (args.printSlot) {
      console.log(ctx.resolveSlot(args.printSlot));
    } else if (args.printJson) {
      console.log(JSON.stringify({
        workspaceRoot: ctx.workspaceRoot,
        targetDevDir: ctx.targetDevDir,
        version: ctx.version,
        slots: ctx.slots,
        semantics: ctx.semantics,
        sources: {
          projectConfig: ctx._projectConfigPath,
          directoryLayout: ctx._directoryLayoutPath,
          routeRegistry: ctx._routeRegistryPath,
        },
      }, null, 2));
    }
  } catch (e) {
    console.error(`❌ ${'$'}{e.message}`);
    process.exit(1);
  }
}
