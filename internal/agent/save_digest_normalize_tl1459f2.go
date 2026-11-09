package agent

import (
	"encoding/json"
	"reflect"
	"unicode/utf8"

	"reasonix/internal/provider"
)

// tl1459F2 口径裁决记档（2026-11-09 用户拍板）：digest 计算算前编码规范化。
//
// digest 是写权限判定与账本指纹（CAS revision ledger）的度量地基，算错 =
// 写权限误判面（安全侧）。本件把「参与 digest 的字符串先做编码规范化」落为
// 显式契约，并修掉一个由对照钉（digest_normalize_tl1459f2_test.go P2）实测
// 抓住的存量指纹分裂：
//
//   - 事实（go1.26 实测）：encoding/json 对字符串里的非法 UTF-8 字节写
//     `\ufffd` 转义序列，但对字面 U+FFFD 字符写原始字节（EF BF BD）。于是
//     「存内含非法字节的原文」与「存储往返后解码出的 U+FFFD 文本」是同一段
//     话的两种表示，旧指纹却把它们算成两个值——ledger 记录的 digest 与重载
//     重算的 digest 天然漂移（历史事故线索「encoding/json 对非法 UTF-8 编码
//     来回不一致=保存主路径纵深定案」的机制根源），写权限 CAS/快照 no-op
//     判据都会误触发。
//
//   - 裁决：digest 输入在进哈希前先规范化——逐 rune 校验 UTF-8 合法性，非法
//     字节单元逐个替换为字面 U+FFFD（NormalizeDigestInput）。替换发生在
//     marshal 之前，两种表示归一为同一串字面 U+FFFD，指纹从此表示稳定。
//     选择该口径而非「与编码器转义产物对齐」的理由：转义形态是编码器的实现
//     细节且不可往返（重载后转义还原成字面字符），只有字面 U+FFFD 才是两种
//     表示的公共不动点。
//
//   - 兼容面（对照钉逐行验证）：合法 UTF-8（ASCII/中文 NFC/NFD/emoji/base64
//     图片串）规范化恒等 → digest 零变化，存量账本零迁移零误判（红线）。
//     问题数据的 digest 相对旧算法改变——但旧值本身就是表示漂移的坏值（存内
//     ≠重载），不是可保留的兼容对象；既有 ledgerStale 自愈路径（load 发现
//     ledger 与重算不符 → 首次保存重录 ledger）吸收全部存量坏值，无需迁移。
//
//   - 不做 NFC 归一：NFC/NFD 同为合法编码，归一会改写存量 NFD 会话指纹（破坏
//     「正常数据新旧一致」红线）；且竞品内容指纹均无 Unicode 归一先例（机制
//     准入三问：无竞品对照、无分叉收益证据、保留成本为每次 digest 全文扫描）。
//
//   - json.RawMessage 字段（ProtocolRecovery/ResponsesItems 等）按字节不透明
//     身份处理，不做替换：存储层原样往返，替换即改写存量指纹（走读器只触碰
//     Kind()==String，[]byte 天然跳过）。
//
// 改根不改梢：规范化只作用于 digest 输入拷贝（digestInputLine /
// digestIdentityBytes）；messagesEqualForStorage 热路径与全部存储写入保持
// 原值——存储原文（Messages 内存值、事件日志、检查点字节）一律不触碰。
//
// 指纹对照钉：digest_normalize_tl1459f2_test.go——正常数据新旧一致（红线）；
// 问题数据差异可解释（旧值=表示漂移坏值，新值=表示稳定正解）。

// NormalizeDigestInput coerces s to valid UTF-8, replacing each invalid byte
// unit with one literal U+FFFD. Fast path: valid input returns s unchanged
// (no scan-to-copy). Exported for internal/control's raw-byte fingerprint
// (v4 bridge transcriptDigest); the agent-internal digest sites go through
// digestInputLine / digestIdentityBytes.
func NormalizeDigestInput(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	out := make([]byte, 0, len(s)+8)
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			out = append(out, c)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// 字面 U+FFFD（EF BF BD）——不是编码器的 `\ufffd` 转义：字面
			// 字符才是存内视图与重载视图的公共不动点（见裁决首条事实）。
			out = append(out, 0xEF, 0xBF, 0xBD)
			i++
			continue
		}
		out = append(out, s[i:i+size]...)
		i += size
	}
	return string(out)
}

// digestInputLine assembles the bytes one digest site hashes for m: the
// identity fold plus encoding normalization (tl1459F2). The six digest
// assembly points feed THIS to their encoder — never the raw value — so the
// hashed bytes no longer depend on whether a message reached memory as raw
// invalid bytes or as the U+FFFD text a storage round trip produces.
func digestInputLine(m provider.Message) ([]byte, error) {
	raw := messageForSessionIdentity(m)
	normalizeDigestStrings(reflect.ValueOf(&raw).Elem(), 0)
	return json.Marshal(raw)
}

// digestIdentityBytes assembles the two line forms the digest+size sites need:
// digestLine is the encoding-normalized form (the hash input, tl1459F2);
// storageLine is the raw identity marshal — the byte form actually written to
// disk, and the size 口径 the event-log fold gate and incremental digestSize
// measure. They differ only on invalid-UTF-8 content, and only there does the
// second marshal run.
func digestIdentityBytes(m provider.Message) (digestLine, storageLine []byte, err error) {
	raw := messageForSessionIdentity(m)
	storageLine, err = json.Marshal(raw)
	if err != nil {
		return nil, nil, err
	}
	if !normalizeDigestStrings(reflect.ValueOf(&raw).Elem(), 0) {
		return storageLine, storageLine, nil
	}
	digestLine, err = json.Marshal(raw)
	if err != nil {
		return nil, nil, err
	}
	return digestLine, storageLine, nil
}

// normalizeDigestStrings walks v and rewrites every invalid-UTF-8 string leaf
// in place (the caller owns a private copy), reporting whether anything
// changed. Strings are validated first, so the overwhelmingly common valid
// case pays one utf8 scan per leaf and no allocation. []byte fields
// (json.RawMessage and friends) are excluded by the byte-opaque ruling: the
// walker only touches Kind()==String. Unexported fields are walked for
// structure but never written (CanSet guard), and depth bounds pathological
// nesting; provider.Message is a flat DTO.
func normalizeDigestStrings(v reflect.Value, depth int) bool {
	if depth > 24 {
		return false
	}
	changed := false
	switch v.Kind() {
	case reflect.String:
		if s := v.String(); !utf8.ValidString(s) && v.CanSet() {
			v.SetString(NormalizeDigestInput(s))
			changed = true
		}
	case reflect.Pointer:
		if !v.IsNil() {
			changed = normalizeDigestStrings(v.Elem(), depth+1)
		}
	case reflect.Interface:
		if !v.IsNil() {
			changed = normalizeDigestStrings(v.Elem(), depth+1)
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return false // []byte / json.RawMessage：字节不透明身份，裁决排除项
		}
		for i := range v.Len() {
			if normalizeDigestStrings(v.Index(i), depth+1) {
				changed = true
			}
		}
	case reflect.Array:
		for i := range v.Len() {
			if normalizeDigestStrings(v.Index(i), depth+1) {
				changed = true
			}
		}
	case reflect.Map:
		for iter := v.MapRange(); iter.Next(); {
			if normalizeDigestStrings(iter.Value(), depth+1) {
				changed = true
			}
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if normalizeDigestStrings(v.Field(i), depth+1) {
				changed = true
			}
		}
	}
	return changed
}
