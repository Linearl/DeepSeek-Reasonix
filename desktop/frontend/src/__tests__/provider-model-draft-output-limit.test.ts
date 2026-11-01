import assert from "node:assert/strict";
// tl1313（20261022 实锤）：输出上限对照模型上下文窗口的设置面校验——
// 输出 > 已设窗口 ⇒ "outputTooLarge"（拒绝应用，三语提示在
// providerSettingsLocale 的 providerUI.validation.outputTooLarge）。
// 发送端 admission 钳制是运行时兜底，设置面必须在写入前挡住这对矛盾；
// 窗口未设（空/0 = 继承）时无从对照，保持放行（后端钳制同样不钳）。
import { modelDraftError } from "../lib/providerModelDraft";

const ok = modelDraftError({ model: "m", contextWindow: "1048576", maxOutputTokens: 32768 }, []);
assert.equal(ok, null, "healthy pair (output <= context) must pass");

// 20261022 事故形态：输出上限大于窗口。
assert.equal(
  modelDraftError({ model: "m", contextWindow: "1048576", maxOutputTokens: 4000000 }, []),
  "outputTooLarge",
  "output > context must be refused as outputTooLarge",
);

// 恰好等于窗口：合法（预算=全窗，发送端 admission 负责留余量）。
assert.equal(
  modelDraftError({ model: "m", contextWindow: "1048576", maxOutputTokens: 1048576 }, []),
  null,
  "output == context must pass",
);

// 窗口未设（继承）：无从对照，不因大小拒绝。
assert.equal(
  modelDraftError({ model: "m", contextWindow: "", maxOutputTokens: 4000000 }, []),
  null,
  "unset context window must not trigger outputTooLarge",
);

// -1（不下发可选限制）与既有 output 语义不变。
assert.equal(modelDraftError({ model: "m", contextWindow: "1048576", maxOutputTokens: -1 }, [], undefined), null, "-1 omit marker must pass");
assert.equal(modelDraftError({ model: "m", contextWindow: "0", maxOutputTokens: 0 }, []), "output", "explicit 0 output keeps the existing output error");

// 既有校验语义不回归：必填 / 重复 / 语法 / 上下文负数。
assert.equal(modelDraftError({ model: "", contextWindow: "1000", maxOutputTokens: 10 }, []), "required");
assert.equal(modelDraftError({ model: "m", contextWindow: "1000", maxOutputTokens: 10 }, ["m"]), "duplicate");
assert.equal(modelDraftError({ model: "a b", contextWindow: "1000", maxOutputTokens: 10 }, []), "syntax");
assert.equal(modelDraftError({ model: "m", contextWindow: "-5", maxOutputTokens: 10 }, []), "context");

console.log("provider-model-draft-output-limit: all assertions passed");
