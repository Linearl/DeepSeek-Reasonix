// Run: tsx src/__tests__/attachment-display.test.ts

import { baseName, formatAttachmentRefForDisplay, formatAttachmentRefForSubmit, parseAttachmentRefsForDisplay, replaceAttachmentRefsForDisplay, restoreAttachmentRefsForSubmit, sortDisplayAttachments } from "../lib/attachmentDisplay";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (JSON.stringify(a) === JSON.stringify(b)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

console.log("\nattachment display");

const named = parseAttachmentRefsForDisplay(
  "review @[DS30000.sl2](.reasonix/attachments/clipboard-20260610-121238.444775-000002.sl2) and @[park.png](.reasonix/attachments/clipboard-20260610-121238.444775-000001.png)",
);

eq(named.text, "review and", "removes named display refs from message text");
eq(
  named.attachments.map((a) => ({ path: a.path, name: a.name, kind: a.kind, ext: a.ext })),
  [
    {
      path: ".reasonix/attachments/clipboard-20260610-121238.444775-000002.sl2",
      name: "DS30000.sl2",
      kind: "file",
      ext: "SL2",
    },
    {
      path: ".reasonix/attachments/clipboard-20260610-121238.444775-000001.png",
      name: "park.png",
      kind: "image",
      ext: "PNG",
    },
  ],
  "preserves original display names for attachment cards",
);
eq(
  sortDisplayAttachments(named.attachments).map((a) => a.name),
  ["park.png", "DS30000.sl2"],
  "sorts images before files while keeping groups stable",
);
eq(
  replaceAttachmentRefsForDisplay("see @[DS30000.sl2](.reasonix/attachments/clipboard-20260610-121238.444775-000002.sl2)"),
  "see [file:DS30000.sl2]",
  "compact previews use named display refs",
);
eq(
  restoreAttachmentRefsForSubmit("review @[DS30000.sl2](.reasonix/attachments/clipboard-20260610-121238.444775-000002.sl2), then @[park.png](.reasonix/attachments/clipboard-20260610-121238.444775-000001.png)"),
  "review @.reasonix/attachments/clipboard-20260610-121238.444775-000002.sl2, then @.reasonix/attachments/clipboard-20260610-121238.444775-000001.png",
  "restores named display refs for submit",
);
eq(
  formatAttachmentRefForDisplay({ path: ".reasonix/attachments/clipboard-20260610-121238.444775-000001.png", name: "park.png", source: "attachment" }),
  "@[park.png](.reasonix/attachments/clipboard-20260610-121238.444775-000001.png)",
  "formats attachment display refs for edit replay",
);
eq(
  formatAttachmentRefForSubmit({ path: ".reasonix/attachments/clipboard-20260610-121238.444775-000001.png" }),
  "@.reasonix/attachments/clipboard-20260610-121238.444775-000001.png",
  "formats raw attachment refs for edit replay submit",
);
eq(baseName("C:\\Users\\Abyss\\Desktop\\DS30000.sl2"), "DS30000.sl2", "extracts Windows path basenames");
eq(baseName("/Users/abyss/Desktop/park.png"), "park.png", "extracts POSIX path basenames");

const escaped = parseAttachmentRefsForDisplay("look at @docs/my\\ report\\ 2026.pdf now");
eq(escaped.text, "look at now", "removes an escaped workspace ref from message text");
eq(
  escaped.attachments.map((a) => ({ path: a.path, name: a.name, kind: a.kind, source: a.source })),
  [{ path: "docs/my report 2026.pdf", name: "my report 2026.pdf", kind: "file", source: "workspace" }],
  "escaped workspace ref becomes one full badge with the unescaped path",
);
eq(
  formatAttachmentRefForDisplay({ path: "docs/my report 2026.pdf", name: "my report 2026.pdf", source: "workspace" }),
  "@docs/my\\ report\\ 2026.pdf",
  "workspace display refs escape whitespace for lossless edit replay",
);
eq(
  formatAttachmentRefForSubmit({ path: "docs/my report 2026.pdf" }),
  "@docs/my\\ report\\ 2026.pdf",
  "workspace submit refs escape whitespace so @-token parsing resolves them",
);

console.log("\ntl1347 injected reference xml fallback");

// tl1347 现场形态：mid-turn steer 注入体 = "Referenced context:" 前导 + 多个
// <image path=...> XML 块 + 用户正文（含 @ref）。兜底层必须把 XML 块从用户
// 视图剥掉，正文与 @ref 缩略图解析不受影响。
const leaked = parseAttachmentRefsForDisplay(
  "Referenced context:\n\n" +
    '<image path=".reasonix/attachments/clipboard-20261101-230839.891402-000014.png">\n已附为视觉输入。\n</image>\n\n' +
    '<image path=".reasonix/attachments/clipboard-20261101-230902.302032-000015.png">\n已附为视觉输入。\n</image>\n\n' +
    "1.这个问题是真实的么？2.渲染丢信息太严重了 @.reasonix/attachments/clipboard-20261101-230839.891402-000014.png",
);

eq(leaked.text.includes("<image"), false, "strips injected <image path> xml blocks from the visible text");
eq(leaked.text.includes("已附为视觉输入"), false, "strips the injected block bodies");
eq(
  leaked.text,
  "1.这个问题是真实的么？2.渲染丢信息太严重了",
  "keeps the user body only — preamble label and blank-line pileup gone",
);
eq(
  leaked.attachments.map((a) => a.path),
  [".reasonix/attachments/clipboard-20261101-230839.891402-000014.png"],
  "body @refs still parse into attachment cards",
);

// 只含引用块、无用户正文的 steer：剥完不剩可见文本。
const refsOnly = parseAttachmentRefsForDisplay(
  'Referenced context:\n\n<image path=".reasonix/attachments/shot.png">\n已附为视觉输入。\n</image>',
);
eq(refsOnly.text, "", "a reference-only steer has no visible text left");

// 同一机制的其他注入块（file/dir/resource/audio）一并兜底。
const fileBlock = parseAttachmentRefsForDisplay(
  'Referenced context:\n\n<file path="docs/spec.md">\n# Spec\n…\n</file>\n\n看一下这个',
);
eq(fileBlock.text, "看一下这个", "strips injected <file path> blocks too");
const dirBlock = parseAttachmentRefsForDisplay('<dir path="src">\nmain.ts\nlib/\n</dir>\n\n结构如何？');
eq(dirBlock.text, "结构如何？", "strips injected <dir path> blocks too");
const audioBlock = parseAttachmentRefsForDisplay('<audio path=".reasonix/attachments/note.wav">\n已附为语音输入。\n</audio>\n\n听完再说');
eq(audioBlock.text, "听完再说", "strips injected <audio path> blocks too");

// 保守边界：未闭合的残块不剥（避免误吞正文）；无注入块时文本原样保留
// （空行收拢只发生在确实剥了块的文本上）。
const unclosed = parseAttachmentRefsForDisplay('<image path="x.png">\n没有闭合标签\n再说');
eq(unclosed.text, '<image path="x.png">\n没有闭合标签\n再说', "an unclosed block is left untouched");
const plainMultiline = parseAttachmentRefsForDisplay("第一段\n\n\n\n第二段");
eq(plainMultiline.text, "第一段\n\n\n\n第二段", "legit blank-line formatting without injected blocks is untouched");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
