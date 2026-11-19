#!/usr/bin/env node

/**
 * generate-docs-index.js
 *
 * 扫描 docs 目录下所有 Markdown 文件，生成 docs/INDEX.md 索引文件。
 *
 * 用法：
 *   node generate-docs-index.js [docs目录] [--apply]
 *
 * 参数：
 *   docs目录    要扫描的 docs 目录路径（默认：./docs）
 *   --apply     直接写入文件，否则仅输出到控制台
 *
 * 输出格式：
 *   - 按目录分类组织
 *   - 包含文件标题（从 frontmatter 或首行提取）
 *   - 包含文件描述（从 frontmatter 提取）
 *   - 包含相对路径
 */

const fs = require('fs');
const path = require('path');

// 配置
const CONFIG = {
  docsDir: process.argv[2] || './docs',
  apply: process.argv.includes('--apply'),
  excludePatterns: [
    'node_modules',
    '.git',
    'B.2.done',
    'B.3.cancelled',
    'B.1.paused'
  ],
  indexFile: 'INDEX.md'
};

/**
 * 从 frontmatter 或首行提取标题
 */
function extractTitle(filePath) {
  try {
    const content = fs.readFileSync(filePath, 'utf-8');

    // 尝试从 frontmatter 提取
    const frontmatterMatch = content.match(/^---\n[\s\S]*?description:\s*["']?(.*?)["']?\s*\n[\s\S]*?---/);
    if (frontmatterMatch) {
      return frontmatterMatch[1].trim();
    }

    // 尝试从首行提取
    const firstLine = content.split('\n').find(line => line.trim() && !line.startsWith('#'));
    if (firstLine) {
      return firstLine.trim().substring(0, 80);
    }

    // 使用文件名
    return path.basename(filePath, '.md');
  } catch (error) {
    return path.basename(filePath, '.md');
  }
}

/**
 * 从 frontmatter 提取描述
 */
function extractDescription(filePath) {
  try {
    const content = fs.readFileSync(filePath, 'utf-8');
    const frontmatterMatch = content.match(/^---\n[\s\S]*?description:\s*["']?(.*?)["']?\s*\n[\s\S]*?---/);
    if (frontmatterMatch) {
      return frontmatterMatch[1].trim();
    }
  } catch (error) {
    // 忽略错误
  }
  return '';
}

/**
 * 递归扫描目录
 */
function scanDirectory(dir, baseDir = dir) {
  const results = [];

  if (!fs.existsSync(dir)) {
    return results;
  }

  const items = fs.readdirSync(dir);

  for (const item of items) {
    const fullPath = path.join(dir, item);
    const stat = fs.statSync(fullPath);

    if (stat.isDirectory()) {
      // 排除不需要的目录
      if (CONFIG.excludePatterns.some(pattern => item.includes(pattern))) {
        continue;
      }

      // 递归扫描子目录
      results.push(...scanDirectory(fullPath, baseDir));
    } else if (item.endsWith('.md') && item !== CONFIG.indexFile) {
      const relativePath = path.relative(baseDir, fullPath);
      const title = extractTitle(fullPath);
      const description = extractDescription(fullPath);

      results.push({
        path: relativePath,
        fullPath: fullPath,
        title: title,
        description: description,
        category: path.dirname(relativePath) || 'root'
      });
    }
  }

  return results;
}

/**
 * 按分类组织文件
 */
function organizeByCategory(files) {
  const categories = {};

  for (const file of files) {
    const category = file.category;
    if (!categories[category]) {
      categories[category] = [];
    }
    categories[category].push(file);
  }

  return categories;
}

/**
 * 生成 INDEX.md 内容
 */
function generateIndexContent(categories) {
  let content = '---\ndescription: "docs 目录索引（自动生成）"\n---\n\n';
  content += '# docs 目录索引\n\n';
  content += '> 本文件由 `scripts/generate-docs-index.js` 自动生成，请勿手动编辑。\n\n';
  content += '---\n\n';

  // 按分类排序
  const sortedCategories = Object.keys(categories).sort();

  for (const category of sortedCategories) {
    const files = categories[category];

    // 分类标题
    const categoryTitle = category === 'root' ? '根目录' : category.replace(/\//g, ' / ');
    content += `## ${categoryTitle}\n\n`;

    // 文件表格
    content += '| 文件 | 标题 | 描述 |\n';
    content += '| --- | --- | --- |\n';

    for (const file of files) {
      const relativePath = `./${file.path}`;
      const description = file.description || '-';
      content += `| [${file.path}](${relativePath}) | ${file.title} | ${description} |\n`;
    }

    content += '\n';
  }

  return content;
}

/**
 * 主函数
 */
function main() {
  console.log(`扫描目录: ${CONFIG.docsDir}`);

  if (!fs.existsSync(CONFIG.docsDir)) {
    console.error(`目录不存在: ${CONFIG.docsDir}`);
    process.exit(1);
  }

  // 扫描文件
  const files = scanDirectory(CONFIG.docsDir);
  console.log(`找到 ${files.length} 个 Markdown 文件`);

  // 按分类组织
  const categories = organizeByCategory(files);

  // 生成内容
  const content = generateIndexContent(categories);

  // 输出或写入
  const indexPath = path.join(CONFIG.docsDir, CONFIG.indexFile);

  if (CONFIG.apply) {
    fs.writeFileSync(indexPath, content, 'utf-8');
    console.log(`已写入: ${indexPath}`);
  } else {
    console.log('\n--- 生成的 INDEX.md ---\n');
    console.log(content);
    console.log('\n--- 使用 --apply 参数写入文件 ---');
  }
}

// 执行
main();
