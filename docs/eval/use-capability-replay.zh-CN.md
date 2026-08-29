# `use_capability` 配对回放准入

Reasonix 1.33.0 的速度与质量发布门槛必须使用真实的配对模型运行。
`internal/eval/replay/testdata/paired_runs.json` 只是测试中位数计算器的合成夹具，
发布门槛会明确拒绝它，不能再把固定数字当成性能证据。

## 实跑要求

基线与候选版本必须在相同模型、effort、工作区、分支、技能、Agent 和 MCP 配置下
运行同一固定任务集。使用一次性的 `REASONIX_HOME` 和 `REASONIX_CACHE_HOME`，至少
完成五对运行。结果文件不得保存提示词、凭据、工具参数或机器本地路径。

每一侧记录以下无内容指标：

- 总时长、总 Token、缓存命中率、主模型回合；
- 工具参数失败、无效远端调用、澄清次数；
- 候选版本的质量判定：发现数据源矛盾、尊重用户决定、没有悬而未决的实现选择、
  代码锚点与验证方式正确。

发布数据集是一个包含 `evidence_kind: "live_paired"`、`model`、`task_set` 和
`pairs` 的对象。每一对包含 `name`、`baseline`、`candidate`，字段名与
`internal/eval/replay/median.go` 中的 `replay.ReleaseRun` 一致。

## 阻塞门槛

执行：

```bash
go run ./internal/eval/replay/cmd/gate -input /absolute/path/to/live-paired-runs.json
```

数据缺失、使用合成夹具或任一指标未达标时都会以非零状态退出。门槛为：

- 至少五对唯一的真实运行；
- 中位总时长至少降低 40%；
- 中位总 Token 至少降低 35%；
- 中位缓存命中率下降不超过 2 个百分点；
- 中位主模型回合不超过 12；
- 候选版本无效远端调用和参数失败均为 0；
- 每次候选运行澄清不超过 1 次；
- 四项质量判定全部通过。

报告同时输出候选版本的时长与 Token P90。第一方原生 Tool Search 仍默认关闭，
不纳入本轮准入。
