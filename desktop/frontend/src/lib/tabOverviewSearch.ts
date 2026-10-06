// 任务 552:标签页概览面板的搜索与排序。
//
// 与 zcode 的 sidePaneTabSearch.ts 同一套算法约定(等价实现,数据源不同):
//   - 查询按空白切词,每个词都必须命中(AND 过滤),否则整条淘汰;
//   - 五档加权:标题前缀 120 / 标题词前缀 90(按 [\s/_.:-]+ 分词)/
//     标题包含 70 / hint 40 / typeLabel 20 / 兜底 1(仅保证 AND 已命中);
//   - 分数降序、同分保持原列表顺序(稳定排序,排序键带上原始下标)。
// hint/typeLabel 的语义见 tabOverviewModel.ts:hint 放会话 id、sessionPath
// 等排查标识(用户手里往往只有它),typeLabel 放 localized 类型词。

export interface TabSearchFields {
  title: string;
  hint: string;
  typeLabel: string;
  /** title+hint+typeLabel 的合并文本,AND 过滤只看它。 */
  all: string;
}

export function normalizeTabSearchText(value: string): string {
  return value.trim().toLocaleLowerCase();
}

export function buildTabSearchFields(title: string, hint: string, typeLabel: string): TabSearchFields {
  return {
    title: normalizeTabSearchText(title),
    hint: normalizeTabSearchText(hint),
    typeLabel: normalizeTabSearchText(typeLabel),
    all: normalizeTabSearchText(`${title} ${hint} ${typeLabel}`),
  };
}

export function normalizeTabSearchQuery(query: string): string[] {
  return normalizeTabSearchText(query).split(/\s+/).filter(Boolean);
}

export function filterAndRankTabSearchItems<T extends { searchFields: TabSearchFields }>(
  items: T[],
  queryParts: string[],
): T[] {
  if (queryParts.length === 0) {
    return items;
  }

  return items
    .map((item, index) => ({
      item,
      index,
      score: tabSearchScore(item.searchFields, queryParts),
    }))
    .filter((entry) => entry.score > 0)
    .sort((left, right) => right.score - left.score || left.index - right.index)
    .map((entry) => entry.item);
}

function tabSearchScore(fields: TabSearchFields, queryParts: string[]): number {
  if (!queryParts.every((part) => fields.all.includes(part))) {
    return 0;
  }

  return queryParts.reduce((score, part) => {
    if (fields.title.startsWith(part)) {
      return score + 120;
    }
    if (hasTabSearchWordPrefix(fields.title, part)) {
      return score + 90;
    }
    if (fields.title.includes(part)) {
      return score + 70;
    }
    if (fields.hint.includes(part)) {
      return score + 40;
    }
    if (fields.typeLabel.includes(part)) {
      return score + 20;
    }
    return score + 1;
  }, 0);
}

function hasTabSearchWordPrefix(value: string, part: string): boolean {
  return value.split(/[\s/_.:-]+/).some((word) => word.startsWith(part));
}
