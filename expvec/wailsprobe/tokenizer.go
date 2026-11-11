package main

// tokenizer.go —— 简化版 Chinese BERT tokenizer（WordPiece）。
// 口径说明（报告需如实引用）：
//   bge-small-zh-v1.5 的官方 tokenizer 是 BertTokenizer(do_lower_case=true, tokenize_chinese_chars=true)。
//   本实现按 BERT 官方 basic+wordpiece 逻辑做 Go 移植：lowercase、CJK 逐字切分、
//   标点独立切分、greedy longest-match wordpiece、[UNK] 兜底。
//   未经 HuggingFace tokenizers 输出逐 id 交叉验证（如本机无 python 环境），自洽性检查见 selfcheck 模式。

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"
)

const (
	unkToken = "[UNK]"
	clsToken = "[CLS]"
	sepToken = "[SEP]"
	padToken = "[PAD]"
)

type Tokenizer struct {
	vocab  map[string]int
	unkID  int
	clsID  int
	sepID  int
	padID  int
	MaxLen int
}

func LoadTokenizer(vocabPath string, maxLen int) (*Tokenizer, error) {
	f, err := os.Open(vocabPath)
	if err != nil {
		return nil, fmt.Errorf("打开词表失败: %w", err)
	}
	defer f.Close()
	vocab := make(map[string]int, 25000)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	idx := 0
	for sc.Scan() {
		tok := sc.Text()
		if _, dup := vocab[tok]; !dup {
			vocab[tok] = idx
		}
		idx++
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读词表失败: %w", err)
	}
	if len(vocab) == 0 {
		return nil, fmt.Errorf("词表为空: %s", vocabPath)
	}
	t := &Tokenizer{vocab: vocab, MaxLen: maxLen}
	t.unkID = vocab[unkToken]
	t.clsID = vocab[clsToken]
	t.sepID = vocab[sepToken]
	t.padID = vocab[padToken]
	if _, ok := vocab[clsToken]; !ok {
		return nil, fmt.Errorf("词表缺少 [CLS]，疑似 vocab.txt 损坏")
	}
	return t, nil
}

func isCJK(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x20000 && r <= 0x2A6DF,
		r >= 0x2A700 && r <= 0x2B73F,
		r >= 0x2B740 && r <= 0x2B81F,
		r >= 0x2B820 && r <= 0x2CEAF,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0x2F800 && r <= 0x2FA1F:
		return true
	}
	return false
}

// basicTokenize：近似 BERT BasicTokenizer（lowercase + CJK/标点切分）。
func (t *Tokenizer) basicTokenize(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case isCJK(r):
			flush()
			out = append(out, string(r))
		case unicode.IsSpace(r):
			flush()
		case unicode.IsPunct(r), unicode.IsSymbol(r),
			(r >= 33 && r <= 47) || (r >= 58 && r <= 64) || (r >= 91 && r <= 96) || (r >= 123 && r <= 126):
			flush()
			out = append(out, string(r))
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// wordpiece：greedy longest-match；超长词或无法匹配返回 [UNK]。
func (t *Tokenizer) wordpiece(word string) []int64 {
	runes := []rune(word)
	if len(runes) > 100 {
		return []int64{int64(t.unkID)}
	}
	var ids []int64
	start := 0
	for start < len(runes) {
		end := len(runes)
		matched := false
		for start < end {
			sub := string(runes[start:end])
			if start > 0 {
				sub = "##" + sub
			}
			if id, ok := t.vocab[sub]; ok {
				ids = append(ids, int64(id))
				start = end
				matched = true
				break
			}
			end--
		}
		if !matched {
			return append(ids, int64(t.unkID))
		}
	}
	return ids
}

// Encode：[CLS] tokens [SEP]，截断到 MaxLen（含特殊 token）。
// 返回 ids / attentionMask（非 pad=1；本函数不 pad，mask 全 1）。
func (t *Tokenizer) Encode(text string) (ids []int64) {
	ids = append(ids, int64(t.clsID))
	for _, w := range t.basicTokenize(text) {
		pi := t.wordpiece(w)
		ids = append(ids, pi...)
		if len(ids) >= t.MaxLen-1 {
			break
		}
	}
	ids = append(ids, int64(t.sepID))
	if len(ids) > t.MaxLen {
		ids = ids[:t.MaxLen]
		ids[len(ids)-1] = int64(t.sepID)
	}
	return ids
}

// VocabSize：自洽性检查用。
func (t *Tokenizer) VocabSize() int { return len(t.vocab) }

// DecodeTokens：ids 还原为近似 token 串（## 去掉后拼接），selfcheck 用。
func (t *Tokenizer) DecodeTokens(ids []int64) string {
	inv := make(map[int]string, len(t.vocab))
	for k, v := range t.vocab {
		inv[v] = k
	}
	var b strings.Builder
	for _, id := range ids {
		tok := inv[int(id)]
		if tok == clsToken || tok == sepToken {
			continue
		}
		b.WriteString(strings.TrimPrefix(tok, "##"))
	}
	return b.String()
}
