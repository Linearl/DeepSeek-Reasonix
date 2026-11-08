package main

// bert.go —— ONNX Runtime Go 绑定封装：加载 bge-small-zh-v1.5 ONNX，文本→向量。
// 绑定库：github.com/yalue/onnxruntime_go v1.38.0（DynamicAdvancedSession，非泛型新 API）。
// pooling 口径：BGE 官方语义 = last_hidden_state[:, 0]（CLS）后 L2 normalize。
// 若导出模型缺 last_hidden_state 输出则回退 pooler_output（差异在报告写明）。

import (
	"fmt"
	"math"

	ort "github.com/yalue/onnxruntime_go"
)

type Embedder struct {
	Session     *ort.DynamicAdvancedSession
	InputNames  []string
	OutputNames []string // 仅选中的输出（last_hidden_state 或 pooler_output）
	CLSMode     bool     // true=last_hidden_state[:,0]; false=pooler_output
	Dim         int
	VocabPath   string
	modelPath   string
}

// InitORT：加载 onnxruntime 共享库并初始化环境（全局一次）。
func InitORT(dllPath string) error {
	ort.SetSharedLibraryPath(dllPath)
	return ort.InitializeEnvironment()
}

// NewEmbedder：探测模型输入输出元信息，创建 DynamicAdvancedSession。
func NewEmbedder(modelPath, vocabPath string, maxLen int) (*Embedder, error) {
	e := &Embedder{VocabPath: vocabPath, modelPath: modelPath, CLSMode: true}
	ins, outs, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("读模型 IO 元信息失败: %w", err)
	}
	for _, io := range ins {
		e.InputNames = append(e.InputNames, io.Name)
	}
	// 选输出：优先 last_hidden_state（BGE 官方 CLS pooling 语义）
	dimFromShape := 0
	for _, io := range outs {
		if io.Name == "last_hidden_state" {
			e.OutputNames = []string{io.Name}
			e.CLSMode = true
			if d := int(io.Dimensions[len(io.Dimensions)-1]); d > 0 {
				dimFromShape = d
			}
			break
		}
	}
	if len(e.OutputNames) == 0 {
		for _, io := range outs {
			if io.Name == "pooler_output" {
				e.OutputNames = []string{io.Name}
				e.CLSMode = false
				if d := int(io.Dimensions[len(io.Dimensions)-1]); d > 0 {
					dimFromShape = d
				}
				break
			}
		}
	}
	if len(e.OutputNames) == 0 {
		return nil, fmt.Errorf("模型无 last_hidden_state/pooler_output 输出，实际输出: %v", outNames(outs))
	}
	if dimFromShape > 0 {
		e.Dim = dimFromShape
	} else {
		e.Dim = 512 // bge-small-zh-v1.5 hidden size（shape 为动态 -1 时的兜底）
	}
	s, err := ort.NewDynamicAdvancedSession(modelPath, e.InputNames, e.OutputNames, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 session 失败: %w", err)
	}
	e.Session = s
	return e, nil
}

func outNames(infos []ort.InputOutputInfo) []string {
	var ns []string
	for _, io := range infos {
		ns = append(ns, io.Name)
	}
	return ns
}

// buildInputs：把一批 ids pad 到同长，构造输入 tensors；返回 cleanup。
func (e *Embedder) buildInputs(batch [][]int64) ([]ort.Value, func(), error) {
	maxLen := 0
	for _, ids := range batch {
		if len(ids) > maxLen {
			maxLen = len(ids)
		}
	}
	if maxLen == 0 {
		return nil, nil, fmt.Errorf("空 batch")
	}
	B := len(batch)
	idsArr := make([]int64, B*maxLen)
	maskArr := make([]int64, B*maxLen)
	for i, ids := range batch {
		base := i * maxLen
		for j := 0; j < maxLen; j++ {
			if j < len(ids) {
				idsArr[base+j] = ids[j]
				maskArr[base+j] = 1
			}
		}
	}
	inShape := ort.NewShape(int64(B), int64(maxLen))
	var vals []ort.Value
	var cleanups []func()
	mkTensor := func(data []int64) (ort.Value, error) {
		t, err := ort.NewTensor(inShape, data)
		if err != nil {
			return nil, err
		}
		tt := t
		cleanups = append(cleanups, func() { _ = tt.Destroy() })
		return t, nil
	}
	for _, name := range e.InputNames {
		var v ort.Value
		var err error
		switch name {
		case "input_ids":
			v, err = mkTensor(idsArr)
		case "attention_mask":
			v, err = mkTensor(maskArr)
		case "token_type_ids":
			v, err = mkTensor(make([]int64, B*maxLen)) // 全 0
		default:
			runCleanups(cleanups)
			return nil, nil, fmt.Errorf("未知输入名: %s", name)
		}
		if err != nil {
			runCleanups(cleanups)
			return nil, nil, fmt.Errorf("构造输入 %s 失败: %w", name, err)
		}
		vals = append(vals, v)
	}
	return vals, func() { runCleanups(cleanups) }, nil
}

func runCleanups(fs []func()) {
	for _, f := range fs {
		f()
	}
}

// EncodeBatch：批编码，返回 batch×dim（CLS/pooler pooling 后已 L2 normalize）。
func (e *Embedder) EncodeBatch(texts []string, tok *Tokenizer) ([][]float32, error) {
	batch := make([][]int64, len(texts))
	for i, t := range texts {
		batch[i] = tok.Encode(t)
	}
	inputs, cleanup, err := e.buildInputs(batch)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// 预分配输出 tensor
	B := len(batch)
	seq := 0
	for _, ids := range batch {
		if len(ids) > seq {
			seq = len(ids)
		}
	}
	var outShape ort.Shape
	if e.CLSMode {
		outShape = ort.NewShape(int64(B), int64(seq), int64(e.Dim))
	} else {
		outShape = ort.NewShape(int64(B), int64(e.Dim))
	}
	outT, err := ort.NewEmptyTensor[float32](outShape)
	if err != nil {
		return nil, fmt.Errorf("分配输出 tensor 失败: %w", err)
	}
	defer func() { _ = outT.Destroy() }()

	if err := e.Session.Run(inputs, []ort.Value{outT}); err != nil {
		return nil, fmt.Errorf("推理失败: %w", err)
	}
	data := outT.GetData()
	H := int64(e.Dim)
	stride := H
	if e.CLSMode {
		stride = int64(seq) * H
	}
	if int64(len(data)) < stride*int64(B) {
		return nil, fmt.Errorf("输出数据量不足: got %d want %d", len(data), stride*int64(B))
	}
	res := make([][]float32, B)
	for i := 0; i < B; i++ {
		off := int64(i) * stride
		vec := make([]float32, H)
		copy(vec, data[off:off+H])
		l2normalize(vec)
		res[i] = vec
	}
	return res, nil
}

func l2normalize(v []float32) {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	n := float32(math.Sqrt(s))
	if n > 0 {
		for i := range v {
			v[i] /= n
		}
	}
}

// cosine：已 normalize 的向量点积。
func cosine(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}
