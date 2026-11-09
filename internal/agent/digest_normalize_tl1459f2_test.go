package agent

// tl1459F2 指纹对照钉（验收核心）。口径裁决见 save_digest_normalize_tl1459f2.go。
//
// 验收矩阵（每条钉=可证伪断言）：
//
//	P1 正常数据（ASCII/中文/emoji/base64 图片串/NFC/NFD）新旧 digest 全等
//	   ——存量账本零迁移零误判（兼容红线）。
//	P2 问题数据（非法 UTF-8 全型）：digest 在「存内原始字节视图」与「存储
//	   往返 U+FFFD 视图」间稳定——修正前两者分裂（对照钉实测的存量 bug，
//	   ledger digest 与重载重算漂移 = 写权限 CAS 误判面）；且新值与旧值
//	   相异（差异可解释：旧值=表示漂移坏值，存量由 ledgerStale 自愈路径吸收）。
//	P3 NormalizeDigestInput：合法输入恒等；非法输入产出合法 UTF-8（逐非法
//	   字节一个字面 U+FFFD）；编码器不对称性入钉（marshal 对非法字节写
//	   `\ufffd` 转义、对字面 U+FFFD 写原始字节——不对称正是本件要修的根因）。
//	P4 六个指纹面对同一语料（含问题数据）digest 全等——写权限判定/账本指纹/
//	   前缀深度/去重指纹/DAG 链共用同一字节流，永不分叉。
//	P5 增量流（923）与全量 digest 在问题数据上全等（fail-open 契约的补强钉）。
//	P6 NFC 与 NFD 同文指纹相异——按字节身份语义，不做 Unicode 归一（归一即
//	   破坏 P1 红线，可解释）。
//	P7 RawMessage 字段字节不透明：非法字节原样过编码器、往返后 digest 不变
//	   （裁决排除项）。
//
// 写权限判定/账本指纹既有钉族回归由包内既有测试承载；本件对合法数据的指纹
// 零变化（P1 成立是其前提）。
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"unicode/utf8"

	"reasonix/internal/provider"
)

type tl1459f2Row struct {
	name    string
	content string
}

// tl1459f2Corpus is the pin dataset: normal rows (P1) and problem rows (P2),
// shared by every pin test so the对照表 stays one dataset.
func tl1459f2Corpus() []tl1459f2Row {
	return []tl1459f2Row{
		{name: "ascii", content: "hello world"},
		{name: "zh", content: "中文会话与账本指纹钉"},
		{name: "latin-nfc", content: "caf\u00e9 r\u00e9sum\u00e9"},
		{name: "latin-nfd", content: "cafe\u0301 re\u0301sume\u0301"},
		{name: "emoji", content: "locked \U0001F512 done"},
		{name: "img-base64", content: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg=="},
		{name: "bad-lone-ff", content: "前\xff后"},
		{name: "bad-truncated", content: "\xe4\xbc"},
		{name: "bad-surrogate-enc", content: "abc\xed\xa0\x80"},
		{name: "bad-truncated-4byte", content: "\xf0\x9f"},
		{name: "bad-stranded-continuation", content: "\x80\x80abc"},
	}
}

func tl1459f2ProblemRows() []tl1459f2Row {
	var out []tl1459f2Row
	for _, row := range tl1459f2Corpus() {
		if !utf8.ValidString(row.content) {
			out = append(out, row)
		}
	}
	return out
}

// tl1459f2LegacyDigest is the pre-change digest formula (identity fold +
// json.Marshal of the raw value), kept verbatim: P1 asserts the production
// funnel equals it row by row on valid data, so any future change to the
// digest byte stream breaks a named pin instead of silently moving ledger
// identity.
func tl1459f2LegacyDigest(msgs []provider.Message) ([sha256.Size]byte, error) {
	h := sha256.New()
	for _, m := range msgs {
		b, err := json.Marshal(messageForSessionIdentity(m))
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		if _, err := h.Write(b); err != nil {
			return [sha256.Size]byte{}, err
		}
		if _, err := h.Write([]byte{'\n'}); err != nil {
			return [sha256.Size]byte{}, err
		}
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

// P1: 正常数据新旧 digest 全等（存量账本兼容红线）。
func TestTl1459F2DigestNewOldAgreementPins(t *testing.T) {
	for _, row := range tl1459f2Corpus() {
		if !utf8.ValidString(row.content) {
			continue // 问题数据归 P2：旧值是表示漂移坏值，新值有意相异
		}
		msgs := []provider.Message{{Role: provider.RoleUser, Content: row.content}}
		want, err := tl1459f2LegacyDigest(msgs)
		if err != nil {
			t.Fatalf("%s: legacy digest: %v", row.name, err)
		}
		got, err := digestSessionMessages(msgs)
		if err != nil {
			t.Fatalf("%s: production digest: %v", row.name, err)
		}
		if got != want {
			t.Errorf("%s: valid-content digest drifted from the pre-change formula: new=%x old=%x (存量账本兼容红线)", row.name, got, want)
		}
	}
}

// P2: 问题数据 digest 表示稳定 + 与旧值的差异可解释。
func TestTl1459F2ProblemDataDigestRepresentationStablePins(t *testing.T) {
	for _, row := range tl1459f2ProblemRows() {
		in := []provider.Message{{Role: provider.RoleUser, Content: row.content}}
		inMemory, err := digestSessionMessages(in)
		if err != nil {
			t.Fatalf("%s: %v", row.name, err)
		}
		// 存储往返：writeSessionMessages/事件记录编码的就是整值 marshal 行，
		// 重载解码回来的 U+FFFD 视图必须算出同一指纹（修正前分裂 = CAS 误判面）。
		line, err := json.Marshal(in[0])
		if err != nil {
			t.Fatalf("%s: marshal storage line: %v", row.name, err)
		}
		var out provider.Message
		if err := json.Unmarshal(line, &out); err != nil {
			t.Fatalf("%s: unmarshal storage line: %v", row.name, err)
		}
		reloaded, err := digestSessionMessages([]provider.Message{out})
		if err != nil {
			t.Fatalf("%s: %v", row.name, err)
		}
		if inMemory != reloaded {
			t.Errorf("%s: digest split across representations: memory=%x reloaded=%x", row.name, inMemory, reloaded)
		}
		legacy, err := tl1459f2LegacyDigest(in)
		if err != nil {
			t.Fatalf("%s: legacy: %v", row.name, err)
		}
		if inMemory == legacy {
			t.Errorf("%s: problem-data digest unexpectedly equals the pre-change value; the explainable-difference pin is void", row.name)
		}
	}
}

// P3: NormalizeDigestInput 行为钉 + 编码器不对称性（本件根因）入钉。
func TestTl1459F2NormalizeDigestInputPins(t *testing.T) {
	if got := NormalizeDigestInput("a\xffb"); got != "a\uFFFDb" {
		t.Errorf("simple coercion: got %q, want a+U+FFFD+b", got)
	}
	if got := NormalizeDigestInput("abc\xed\xa0\x80"); got != "abc\uFFFD\uFFFD\uFFFD" {
		t.Errorf("surrogate encoding: got %q, want abc + three U+FFFD (one per invalid byte)", got)
	}
	for _, row := range tl1459f2Corpus() {
		got := NormalizeDigestInput(row.content)
		if !utf8.ValidString(got) {
			t.Errorf("%s: normalize produced invalid UTF-8", row.name)
		}
		if utf8.ValidString(row.content) {
			if got != row.content {
				t.Errorf("%s: valid input must pass through unchanged", row.name)
			}
			continue
		}
		// 编码器不对称性：非法字节被 marshal 成 `\ufffd` 转义，字面 U+FFFD
		// 被写成原始字节——归一前后 marshal 产物必然不同。这正是旧指纹随
		// 表示漂移的根因，钉住它防止有人误「修复」回转义形态。
		raw, err := json.Marshal(row.content)
		if err != nil {
			t.Fatalf("%s: marshal raw: %v", row.name, err)
		}
		norm, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("%s: marshal normalized: %v", row.name, err)
		}
		if bytes.Equal(raw, norm) {
			t.Errorf("%s: encoder asymmetry vanished — marshal of normalized equals marshal of raw", row.name)
		}
	}
}

// P4: 六个指纹面同一字节流（含问题数据）。
func TestTl1459F2DigestSurfacesAgree(t *testing.T) {
	var msgs []provider.Message
	for _, row := range tl1459f2Corpus() {
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: row.content})
	}
	full, err := digestSessionMessages(msgs)
	if err != nil {
		t.Fatalf("full pass: %v", err)
	}
	hasher := newSessionTranscriptHasher()
	hasher.addAll(msgs)
	stream, ok := hasher.sum()
	if !ok || stream != full {
		t.Errorf("decode-side hasher disagrees: ok=%v stream=%x full=%x", ok, stream, full)
	}
	if depth := messagesPrefixDigestDepth(msgs, full); depth != len(msgs) {
		t.Errorf("prefix depth %d, want %d (the full digest must be reachable as the final prefix)", depth, len(msgs))
	}
	for i, m := range msgs {
		fp := messageFingerprint(m)
		line, err := digestInputLine(m)
		if err != nil {
			t.Fatalf("row %d digest input line: %v", i, err)
		}
		if fp != string(line) {
			t.Errorf("row %d fingerprint drifted from the normalized digest input line", i)
		}
		chain, err := sessionDAGChainDigest("parent", m)
		if err != nil {
			t.Fatalf("row %d dag chain: %v", i, err)
		}
		sum := sha256.Sum256(append(append([]byte("parent"), 0), line...))
		if chain != hex.EncodeToString(sum[:]) {
			t.Errorf("row %d dag chain drifted from sha256(parent,0,identity)", i)
		}
	}
}

// P5: 增量流（923）与全量 digest 在问题数据上全等，size 口径一致。
func TestTl1459F2IncrementalDigestAgreesOnProblemData(t *testing.T) {
	s := NewSession("")
	for _, row := range tl1459f2Corpus()[:3] {
		s.Add(provider.Message{Role: provider.RoleUser, Content: row.content})
	}
	msgs, version, rewrite := s.snapshotWithVersion()
	_, size, h, err := digestAndSizeSessionMessagesState(msgs)
	if err != nil {
		t.Fatalf("seed full pass: %v", err)
	}
	s.seedDigestState(msgs, version, rewrite, h, size)
	for _, row := range tl1459f2ProblemRows() {
		s.Add(provider.Message{Role: provider.RoleUser, Content: row.content})
	}
	full, err := digestSessionMessages(s.Snapshot())
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	inc, incSize, ok := s.incrementalDigestAndSize(s.Snapshot(), s.version, s.rewriteVersion)
	if !ok {
		t.Fatalf("incremental state invalid after pure appends")
	}
	if inc != full {
		t.Errorf("incremental digest drifted: inc=%x full=%x", inc, full)
	}
	wantSize, err := sessionMessagesEncodedSize(s.Snapshot())
	if err != nil || incSize != wantSize {
		t.Errorf("incremental size %d, want %d (err=%v)", incSize, wantSize, err)
	}
}

// P6: NFC/NFD 保持相异——无 Unicode 折叠进入指纹。
func TestTl1459F2NfcNfdRemainDistinctPins(t *testing.T) {
	nfc := []provider.Message{{Role: provider.RoleUser, Content: "caf\u00e9"}}
	nfd := []provider.Message{{Role: provider.RoleUser, Content: "cafe\u0301"}}
	a, err := digestSessionMessages(nfc)
	if err != nil {
		t.Fatalf("nfc: %v", err)
	}
	b, err := digestSessionMessages(nfd)
	if err != nil {
		t.Fatalf("nfd: %v", err)
	}
	if a == b {
		t.Fatalf("NFC and NFD digests collided — Unicode folding entered the digest identity")
	}
	if NormalizeDigestInput(nfc[0].Content) != nfc[0].Content || NormalizeDigestInput(nfd[0].Content) != nfd[0].Content {
		t.Fatalf("valid NFC/NFD input must normalize to itself")
	}
}

// P7: RawMessage 字节不透明（裁决排除项）——非法字节原样过编码器，往返后
// digest 不变。
func TestTl1459F2RawMessageFieldsStayByteOpaque(t *testing.T) {
	raw := json.RawMessage("{\"note\":\"\xff\xed\xa0\x80\"}")
	in := provider.Message{Role: provider.RoleUser, Content: "ok", ProtocolRecovery: raw}
	before, err := digestSessionMessages([]provider.Message{in})
	if err != nil {
		t.Fatalf("digest with raw RawMessage: %v", err)
	}
	line, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(line, []byte("\xff")) {
		t.Fatalf("RawMessage invalid bytes were coerced at storage encoding — byte-opaque contract broken")
	}
	var out provider.Message
	if err := json.Unmarshal(line, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	after, err := digestSessionMessages([]provider.Message{out})
	if err != nil {
		t.Fatalf("digest after reload: %v", err)
	}
	if before != after {
		t.Errorf("RawMessage round trip changed the digest: %x -> %x", before, after)
	}
}
