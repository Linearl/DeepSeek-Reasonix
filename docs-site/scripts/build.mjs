#!/usr/bin/env node
// 任务 1102：产品文档静态站构建脚本。
//
// 设计取舍：**零依赖**。任务书要求「站点本身要长期低维护」，所以不用
// VitePress/Docusaurus，只用 Node 标准库把四个目录里的 Markdown 转成一套
// 自带布局的静态 HTML（首页 + 每个目录一个落地页 + 每篇 md 一个页面）。
// Markdown 语法按「文档骨架够用」的子集实现（标题/段落/列表/链接/代码块/
// 表格/行内代码/粗体斜体/引用），不追求 CommonMark 全覆盖——文档作者按
// 骨架写就不会踩边界。后续若内容复杂到需要完整渲染器，再单独引入（届时
// 仅改这一个脚本，站点结构不变）。
//
// 输入：docs-site/{index.md + getting-started,features,reference,troubleshooting}/**/*.md
// 输出：docs-site/dist/（Git 忽略；Pages workflow 每次重新构建）
//
// 每篇 md 支持可选 front-matter（文件顶部的 `<!--meta title=... desc=... -->`），
// 用于指定页面标题与副标题；缺省时从首个 H1 推导。

import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..");
const dist = join(root, "dist");

// ── 站点元信息 ────────────────────────────────────────────────────────────
const SITE = {
  name: "Reasonix 产品文档",
  tagline: "Reasonix —— 面向个人与团队的 AI 编码协作桌面端。",
  baseUrl: process.env.DOCS_BASE_URL || "/DeepSeek-Reasonix/docs-site/",
  repoUrl: "https://github.com/Linearl/DeepSeek-Reasonix",
};

// 目录定义：slug 决定 URL 前缀与 dist 子目录，label/navOrder 决定导航顺序。
const SECTIONS = [
  { slug: "getting-started", label: "快速开始", navOrder: 1, blurb: "安装、首次运行、第一段对话。" },
  { slug: "features", label: "功能", navOrder: 2, blurb: "会话、工作区、扩展、实验室等核心能力。" },
  { slug: "reference", label: "参考", navOrder: 3, blurb: "配置项、命令、工具清单的查表式参考。" },
  { slug: "troubleshooting", label: "疑难排查", navOrder: 4, blurb: "日志位置、常见报错、诊断入口。" },
];

// ── Markdown → HTML（够用子集）────────────────────────────────────────────
function escapeHtml(s) {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

function inline(text) {
  let out = escapeHtml(text);
  // 行内代码先摘出来保护，避免被下面的规则误伤。
  const codeSpans = [];
  out = out.replace(/`([^`]+)`/g, (_, c) => { codeSpans.push(c); return `\u0000${codeSpans.length - 1}\u0000`; });
  out = out.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  out = out.replace(/(^|[\s(])\*([^*\n]+)\*/g, "$1<em>$2</em>");
  // 链接 [text](url)
  out = out.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_, label, href) => {
    const external = /^https?:\/\//.test(href);
    const rel = external ? ' rel="noopener" target="_blank"' : "";
    return `<a href="${href}"${rel}>${label}</a>`;
  });
  out = out.replace(/\u0000(\d+)\u0000/g, (_, i) => `<code>${codeSpans[Number(i)]}</code>`);
  return out;
}

function markdownToHtml(md) {
  const lines = md.replace(/\r\n/g, "\n").split("\n");
  const out = [];
  let i = 0;
  let listType = null; // "ul" | "ol" | null

  const closeList = () => { if (listType) { out.push(`</${listType}>`); listType = null; } };

  while (i < lines.length) {
    const line = lines[i];

    // 代码块
    if (/^```/.test(line)) {
      closeList();
      const lang = line.slice(3).trim();
      const buf = [];
      i += 1;
      while (i < lines.length && !/^```/.test(lines[i])) { buf.push(lines[i]); i += 1; }
      i += 1; // 跳过收尾 ```
      const cls = lang ? ` class="language-${escapeHtml(lang)}"` : "";
      out.push(`<pre><code${cls}>${escapeHtml(buf.join("\n"))}</code></pre>`);
      continue;
    }

    // 空行
    if (/^\s*$/.test(line)) { closeList(); i += 1; continue; }

    // 标题
    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) {
      closeList();
      const level = h[1].length;
      const text = h[2].trim();
      const id = text.toLowerCase().replace(/[^\w\u4e00-\u9fa5]+/g, "-").replace(/^-+|-+$/g, "");
      out.push(`<h${level} id="${id}">${inline(text)}</h${level}>`);
      i += 1;
      continue;
    }

    // 引用块
    if (/^>\s?/.test(line)) {
      closeList();
      const buf = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) { buf.push(lines[i].replace(/^>\s?/, "")); i += 1; }
      out.push(`<blockquote>${inline(buf.join(" "))}</blockquote>`);
      continue;
    }

    // 表格（| a | b | 行 + |---| 分隔行）
    if (/^\|.*\|\s*$/.test(line) && /^\|[\s:|-]+\|\s*$/.test(lines[i + 1] ?? "")) {
      closeList();
      const header = splitRow(line);
      i += 2;
      const rows = [];
      while (i < lines.length && /^\|.*\|\s*$/.test(lines[i])) { rows.push(splitRow(lines[i])); i += 1; }
      let t = "<table><thead><tr>";
      for (const c of header) t += `<th>${inline(c)}</th>`;
      t += "</tr></thead><tbody>";
      for (const r of rows) { t += "<tr>"; for (const c of r) t += `<td>${inline(c)}</td>`; t += "</tr>"; }
      t += "</tbody></table>";
      out.push(t);
      continue;
    }

    // 无序列表
    const ul = /^[-*]\s+(.*)$/.exec(line);
    if (ul) {
      if (listType !== "ul") { closeList(); out.push("<ul>"); listType = "ul"; }
      out.push(`<li>${inline(ul[1])}</li>`);
      i += 1;
      continue;
    }
    // 有序列表
    const ol = /^\d+\.\s+(.*)$/.exec(line);
    if (ol) {
      if (listType !== "ol") { closeList(); out.push("<ol>"); listType = "ol"; }
      out.push(`<li>${inline(ol[1])}</li>`);
      i += 1;
      continue;
    }

    // 段落（连续非空非特殊行合并）
    closeList();
    const buf = [line];
    i += 1;
    while (i < lines.length && !/^\s*$/.test(lines[i]) && !/^(#{1,6})\s|^```|^>|^[-*]\s|^\d+\.\s|^\|/.test(lines[i])) {
      buf.push(lines[i]);
      i += 1;
    }
    out.push(`<p>${inline(buf.join(" "))}</p>`);
  }
  closeList();
  return out.join("\n");
}

function splitRow(row) {
  return row.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((c) => c.trim());
}

// ── front-matter 与文件发现 ───────────────────────────────────────────────
function parseFrontMatter(raw) {
  const m = /^\s*<!--\s*meta\s+([\s\S]*?)-->\s*\n?/.exec(raw);
  if (!m) return { meta: {}, body: raw };
  const meta = {};
  // 值可含空格：优先匹配引号包裹（title="..." / desc="..."），否则匹配到
  // 下一个 key= 或行尾。不这样做会把 "在 Windows 上安装" 截成 "在"。
  const re = /(\w+)=(?:"([^"]*)"|'([^']*)'|([^\s]+(?:\s+(?!\w+=)[^\s]+)*))/g;
  let mm;
  while ((mm = re.exec(m[1])) !== null) {
    const value = mm[2] ?? mm[3] ?? mm[4] ?? "";
    meta[mm[1]] = value.trim();
  }
  return { meta, body: raw.slice(m[0].length) };
}

function firstH1(body) {
  const m = /^#\s+(.*)$/m.exec(body);
  return m ? m[1].trim() : null;
}

function walkMarkdown(dir) {
  const out = [];
  if (!existsSync(dir)) return out;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, entry.name);
    if (entry.isDirectory()) out.push(...walkMarkdown(p));
    else if (entry.name.endsWith(".md")) out.push(p);
  }
  return out.sort();
}

// ── 页面渲染 ──────────────────────────────────────────────────────────────
function navHtml(activeSlug) {
  let nav = "";
  for (const s of [...SECTIONS].sort((a, b) => a.navOrder - b.navOrder)) {
    const cls = s.slug === activeSlug ? ' class="is-active"' : "";
    nav += `<li><a href="${SITE.baseUrl}${s.slug}/"${cls}>${s.label}</a></li>`;
  }
  return nav;
}

function layout({ title, description, activeSlug, body, breadcrumb }) {
  const full = title ? `${title} — ${SITE.name}` : SITE.name;
  return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${escapeHtml(full)}</title>
<meta name="description" content="${escapeHtml(description || SITE.tagline)}">
<link rel="stylesheet" href="${SITE.baseUrl}assets/styles.css">
</head>
<body>
<header class="site-header">
  <div class="wrap">
    <a class="brand" href="${SITE.baseUrl}">${escapeHtml(SITE.name)}</a>
    <nav aria-label="文档目录"><ul>${navHtml(activeSlug)}</ul></nav>
    <a class="repo-link" href="${SITE.repoUrl}" rel="noopener" target="_blank">GitHub</a>
  </div>
</header>
<main class="wrap">
${breadcrumb ? `<nav class="crumbs" aria-label="面包屑">${breadcrumb}</nav>` : ""}
${body}
</main>
<footer class="site-footer"><div class="wrap">${escapeHtml(SITE.name)} · <a href="${SITE.repoUrl}" rel="noopener" target="_blank">GitHub</a></div></footer>
</body>
</html>
`;
}

// ── 构建 ──────────────────────────────────────────────────────────────────
function build() {
  rmSync(dist, { recursive: true, force: true });
  mkdirSync(join(dist, "assets"), { recursive: true });
  cpSync(join(root, "assets", "styles.css"), join(dist, "assets", "styles.css"));

  const pages = [];

  // 首页
  const indexRaw = readFileSync(join(root, "index.md"), "utf8");
  const indexParsed = parseFrontMatter(indexRaw);
  pages.push({
    out: join(dist, "index.html"),
    html: layout({
      title: null,
      description: indexParsed.meta.desc || SITE.tagline,
      activeSlug: null,
      body: `<article class="prose">${markdownToHtml(indexParsed.body)}</article>`,
      breadcrumb: null,
    }),
  });

    // 每个目录：落地页（列出该目录全部文章）+ 每篇文章一个页面
  for (const section of SECTIONS) {
    const sectionDir = join(root, section.slug);
    const files = walkMarkdown(sectionDir);
    const items = files.map((file) => {
      const raw = readFileSync(file, "utf8");
      const { meta, body } = parseFrontMatter(raw);
      const rel = relative(sectionDir, file).split(sep).join("/");
      const urlBase = rel.replace(/\.md$/, "");
      const title = meta.title || firstH1(body) || urlBase;
      return { title, desc: meta.desc || "", body, url: `${SITE.baseUrl}${section.slug}/${urlBase}.html`, out: join(dist, section.slug, `${urlBase}.html`) };
    });

    // 目录落地页
    let listing = `<h1>${section.label}</h1><p class="lede">${section.blurb}</p><ul class="page-list">`;
    for (const it of items) {
      listing += `<li><a href="${it.url}">${escapeHtml(it.title)}</a>${it.desc ? `<p>${escapeHtml(it.desc)}</p>` : ""}</li>`;
    }
    listing += "</ul>";
    pages.push({
      out: join(dist, section.slug, "index.html"),
      html: layout({
        title: section.label,
        description: section.blurb,
        activeSlug: section.slug,
        body: `<article class="prose">${listing}</article>`,
        breadcrumb: `<a href="${SITE.baseUrl}">首页</a> / ${escapeHtml(section.label)}`,
      }),
    });

    // 文章页
    for (const it of items) {
      pages.push({
        out: it.out,
        html: layout({
          title: it.title,
          description: it.desc,
          activeSlug: section.slug,
          body: `<article class="prose">${markdownToHtml(it.body)}</article>`,
          breadcrumb: `<a href="${SITE.baseUrl}">首页</a> / <a href="${SITE.baseUrl}${section.slug}/">${section.label}</a> / ${escapeHtml(it.title)}`,
        }),
      });
    }
  }

  for (const page of pages) {
    mkdirSync(dirname(page.out), { recursive: true });
    writeFileSync(page.out, page.html, "utf8");
  }

  // 404
  writeFileSync(join(dist, "404.html"), layout({
    title: "页面未找到",
    description: "404",
    activeSlug: null,
    body: `<article class="prose"><h1>页面未找到</h1><p>你访问的文档页面不存在。<a href="${SITE.baseUrl}">回到首页</a>。</p></article>`,
    breadcrumb: null,
  }), "utf8");

  const bytes = pages.reduce((n, p) => n + statSync(p.out).size, 0);
  console.log(`docs-site: built ${pages.length} pages (${(bytes / 1024).toFixed(1)} KB) into ${relative(process.cwd(), dist) || "dist"}`);
}

build();
