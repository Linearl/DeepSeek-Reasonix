package main

// retrieve.go —— 召回对照实验：向量 top5 vs 关键词 top5（模拟记忆库语料）。
// 关键词路口径（近似产品 grep/子串匹配语义，写报告时需引用）：
//   score = 1.0（query 归一化整串是文档子串，强命中）
//         + 0.5 × (query 字符集中被文档命中的比例)（弱信号，无整串命中时主导）
//   排序：score 降序，同分按语料序号升序。

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
)

type corpusDoc struct {
	id   string
	text string
}

func readCorpus(path string) ([]corpusDoc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var docs []corpusDoc
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("corpus 第 %d 行格式异常(需 id<TAB>text): %s", lineNo, line)
		}
		docs = append(docs, corpusDoc{id: parts[0], text: parts[1]})
	}
	return docs, sc.Err()
}

func normQuery(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// keywordScore：关键词检索基线打分。
func keywordScore(query, doc string) float64 {
	q := normQuery(query)
	if q == "" {
		return 0
	}
	score := 0.0
	if strings.Contains(doc, q) {
		score += 1.0
	}
	// 字符级重合（query 去重字符集中被 doc 命中的比例）
	seen := map[rune]bool{}
	var uniq []rune
	for _, r := range q {
		if !seen[r] {
			seen[r] = true
			uniq = append(uniq, r)
		}
	}
	hit := 0
	for _, r := range uniq {
		if strings.ContainsRune(doc, r) {
			hit++
		}
	}
	score += 0.5 * float64(hit)/float64(len(uniq))
	return score
}

type hit struct {
	id    string
	score float64
}

func topK(docs []corpusDoc, scoreFn func(corpusDoc) float64, k int) []hit {
	hs := make([]hit, len(docs))
	for i, d := range docs {
		hs[i] = hit{d.id, scoreFn(d)}
	}
	sort.SliceStable(hs, func(i, j int) bool {
		if hs[i].score != hs[j].score {
			return hs[i].score > hs[j].score
		}
		return hs[i].id < hs[j].id
	})
	if len(hs) > k {
		hs = hs[:k]
	}
	return hs
}

func runRetrieve(modelPath, vocabPath, dllPath, corpusPath, queriesPath string) error {
	if err := InitORT(dllPath); err != nil {
		return err
	}
	tok, err := LoadTokenizer(vocabPath, 512)
	if err != nil {
		return err
	}
	emb, err := NewEmbedder(modelPath, vocabPath, 512)
	if err != nil {
		return err
	}
	docs, err := readCorpus(corpusPath)
	if err != nil {
		return err
	}
	fmt.Printf("语料 %d 条，维度 %d，pooling=%v\n", len(docs), emb.Dim, emb.CLSMode)

	// 语料编码（分批防峰值内存）
	const chunk = 32
	docVecs := make([][]float32, len(docs))
	for lo := 0; lo < len(docs); lo += chunk {
		hi := lo + chunk
		if hi > len(docs) {
			hi = len(docs)
		}
		vecs, err := emb.EncodeBatch(textsOf(docs[lo:hi]), tok)
		if err != nil {
			return fmt.Errorf("语料编码失败: %w", err)
		}
		copy(docVecs[lo:hi], vecs)
	}

	// queries
	qf, err := os.Open(queriesPath)
	if err != nil {
		return err
	}
	defer qf.Close()
	var queries []string
	sc := bufio.NewScanner(qf)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		q := strings.TrimSpace(sc.Text())
		if q != "" && !strings.HasPrefix(q, "#") {
			queries = append(queries, q)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	qVecs, err := emb.EncodeBatch(queries, tok)
	if err != nil {
		return fmt.Errorf("query 编码失败: %w", err)
	}

	// 对照输出
	for qi, q := range queries {
		fmt.Printf("\n### Q%d: %s\n", qi+1, q)
		// 向量路
		var vhits []hit
		for i := range docs {
			vhits = append(vhits, hit{docs[i].id, cosine(qVecs[qi], docVecs[i])})
		}
		sort.SliceStable(vhits, func(i, j int) bool { return vhits[i].score > vhits[j].score })
		if len(vhits) > 5 {
			vhits = vhits[:5]
		}
		fmt.Println("  [向量top5]")
		for _, h := range vhits {
			d := docByID(docs, h.id)
			fmt.Printf("    %s  sim=%.4f  %s\n", h.id, h.score, trunc(d, 60))
		}
		// 关键词路
		khits := topK(docs, func(d corpusDoc) float64 { return keywordScore(q, d.text) }, 5)
		fmt.Println("  [关键词top5]")
		for _, h := range khits {
			d := docByID(docs, h.id)
			fmt.Printf("    %s  score=%.4f  %s\n", h.id, h.score, trunc(d, 60))
		}
	}
	return nil
}

func textsOf(docs []corpusDoc) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.text
	}
	return out
}

func docByID(docs []corpusDoc, id string) string {
	for _, d := range docs {
		if d.id == id {
			return d.text
		}
	}
	return ""
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
