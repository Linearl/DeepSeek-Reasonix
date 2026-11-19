export const meta = {
  name: 'test-generation-parallel',
  description: '并行生成后端服务层单元测试，覆盖 26 个服务模块',
  phases: [
    { title: '分析服务结构', detail: '扫描服务模块依赖和导出接口' },
    { title: 'Group 1 生成测试', detail: '核心业务服务：auth, submission, upload, outbound, dvc' },
    { title: 'Group 2 生成测试', detail: '数据处理服务：fiftyone, compliance, monitoring, logs' },
    { title: 'Group 3 生成测试', detail: '基础设施服务：admin, claude, filter, task 等' },
    { title: '运行测试验证', detail: '执行所有新生成的测试' },
  ],
}

const SERVICE_GROUPS = {
  group1_core: {
    name: '核心业务服务',
    modules: ['auth', 'submission', 'upload', 'outbound', 'dvc'],
    priority: 'high',
  },
  group2_data: {
    name: '数据处理服务',
    modules: ['fiftyone', 'compliance', 'monitoring', 'logs', 'feedback'],
    priority: 'medium',
  },
  group3_infra: {
    name: '基础设施服务',
    modules: ['admin', 'claude', 'filter', 'task', 'systemConfig', 'configFile', 'flags', 'hooks', 'models', 'operationExecutor', 'tool', 'uploadMetadata', 'video', 'fileOps', 'metadataScan'],
    priority: 'low',
  },
}

const TEST_TEMPLATE = `// Auto-generated unit test for {{serviceName}}
// Generated: {{date}}

import { {{serviceName}} } from '../{{serviceName}}';

describe('{{serviceName}}', () => {
  let service: {{serviceName}};

  beforeEach(() => {
    service = new {{serviceName}}();
    jest.clearAllMocks();
  });

  describe('constructor', () => {
    it('should create instance', () => {
      expect(service).toBeDefined();
    });
  });

  // TODO: Add tests for each public method
  // Example pattern:
  // it('methodName should return expected result', async () => {
  //   // Arrange
  //   const input = {};
  //
  //   // Act
  //   const result = await service.methodName(input);
  //
  //   // Assert
  //   expect(result).toBeDefined();
  // });
});
`

phase('分析服务结构')
const serviceAnalysis = await agent(
  `分析后端服务层结构，返回每个服务模块的：
1. 主要导出的类和函数
2. 依赖的外部模块（需要 mock）
3. 公开方法列表
4. 建议的测试重点

目标目录：02-dev/dev_12/backend/src/services/

按 Group 分组输出 JSON 格式：
{
  "group1_core": { "auth": {...}, "submission": {...}, ... },
  "group2_data": { ... },
  "group3_infra": { ... }
}`,
  {
    label: 'analyze-services',
    phase: '分析服务结构',
  }
)

phase('并行生成三组测试')
const [group1Tests, group2Tests, group3Tests] = await parallel([
  () => agent(
    `为 Group 1 核心业务服务生成单元测试文件。

服务模块：auth, submission, upload, outbound, dvc
源码目录：02-dev/dev_12/backend/src/services/

要求：
1. 每个服务模块创建 __tests__/ 目录
2. 为每个 .ts 文件创建对应的 .test.ts
3. Mock 所有外部依赖（数据库、Redis、其他服务）
4. 测试所有公开方法的正常路径和异常路径
5. 使用 AAA 模式（Arrange-Act-Assert）
6. 覆盖率目标：statements 70%+

输出格式：
- 生成完整的测试文件内容
- 文件路径：src/services/{module}/__tests__/{filename}.test.ts
- 确保测试可直接运行`,
    {
      label: 'group1-core-tests',
      phase: '并行生成三组测试',
    }
  ),
  () => agent(
    `为 Group 2 数据处理服务生成单元测试文件。

服务模块：fiftyone, compliance, monitoring, logs, feedback
源码目录：02-dev/dev_12/backend/src/services/

要求：
1. 每个服务模块创建 __tests__/ 目录
2. 为每个 .ts 文件创建对应的 .test.ts
3. Mock 所有外部依赖（数据库、Redis、其他服务）
4. 测试所有公开方法的正常路径和异常路径
5. 使用 AAA 模式（Arrange-Act-Assert）
6. 覆盖率目标：statements 70%+

输出格式：
- 生成完整的测试文件内容
- 文件路径：src/services/{module}/__tests__/{filename}.test.ts
- 确保测试可直接运行`,
    {
      label: 'group2-data-tests',
      phase: '并行生成三组测试',
    }
  ),
  () => agent(
    `为 Group 3 基础设施服务生成单元测试文件。

服务模块：admin, claude, filter, task, systemConfig, configFile, flags, hooks, models, operationExecutor, tool, uploadMetadata, video, fileOps, metadataScan
源码目录：02-dev/dev_12/backend/src/services/

要求：
1. 每个服务模块创建 __tests__/ 目录
2. 为每个 .ts 文件创建对应的 .test.ts
3. Mock 所有外部依赖（数据库、Redis、其他服务）
4. 测试所有公开方法的正常路径和异常路径
5. 使用 AAA 模式（Arrange-Act-Assert）
6. 覆盖率目标：statements 70%+

输出格式：
- 生成完整的测试文件内容
- 文件路径：src/services/{module}/__tests__/{filename}.test.ts
- 确保测试可直接运行`,
    {
      label: 'group3-infra-tests',
      phase: '并行生成三组测试',
    }
  ),
])

phase('运行测试验证')
const testValidation = await agent(
  `运行所有新生成的服务层测试，验证测试可执行。

步骤：
1. cd 02-dev/dev_12/backend
2. 运行 npm run test:unit -- --testPathPattern="src/services"
3. 收集测试结果：
   - 通过的测试数量
   - 失败的测试及原因
   - 覆盖率统计
4. 对于失败的测试，分析原因并提供修复建议

输出格式：
{
  "totalTests": number,
  "passed": number,
  "failed": number,
  "coverage": { "statements": number, "branches": number, "functions": number, "lines": number },
  "failures": [{ "file": string, "error": string, "fix": string }],
  "summary": string
}`,
  {
    label: 'validate-tests',
    phase: '运行测试验证',
  }
)

return {
  serviceAnalysis,
  group1Tests,
  group2Tests,
  group3Tests,
  testValidation,
  timestamp: new Date().toISOString(),
}
