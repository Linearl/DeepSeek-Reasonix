<svg xmlns="http://www.w3.org/2000/svg" width="1180" height="620" font-family="Microsoft YaHei, sans-serif">
  <rect width="1180" height="620" fill="#1e1e2e"/>
  <text x="590" y="34" text-anchor="middle" font-size="20" fill="#cdd6f4">{{计划标题 — N Block / M 线 / K 行}}</text>

  <!-- Block 框：x=30 起，每框 350 宽、间距 45（435/840）；描边分色 #89b4fa/#a6e3a1/#cba6f7 -->
  <rect x="30" y="60" width="350" height="530" rx="10" fill="#313244" stroke="#89b4fa" stroke-width="2"/>
  <text x="205" y="90" text-anchor="middle" font-size="16" fill="#89b4fa">{{Block 1 名 — N 线}}</text>
  <!-- 线框（1 任务示例，高 52）：y=110 起步长 62 -->
  <rect x="50" y="110" width="310" height="52" rx="6" fill="#45475a"/>
  <text x="65" y="132" font-size="13" fill="#f9e2af">{{线X · 任务号}}</text>
  <text x="65" y="152" font-size="11" fill="#a6adc8">{{一句话摘要 (规模 S/M/L)}}</text>
  <!-- 复制上面两行 <text> 到同框内可放第 2/3 任务；框高相应改 84/120 -->

  <!-- 闸门箭头（Block 之间，x=385/790） -->
  <text x="405" y="320" text-anchor="middle" font-size="12" fill="#f38ba8">闸门</text>
  <text x="405" y="338" text-anchor="middle" font-size="10" fill="#f38ba8">合并→测试</text>
  <text x="405" y="352" text-anchor="middle" font-size="10" fill="#f38ba8">→审计→过</text>
  <path d="M 385 300 L 425 300 M 420 295 L 430 300 L 420 305" stroke="#f38ba8" stroke-width="2" fill="none"/>

  <!-- 第 2/3 Block 框复制 Block 框结构改 x=435/840 与描边色 -->

  <!-- 底部对账注（必写：行数对账） -->
  <text x="590" y="615" text-anchor="middle" font-size="11" fill="#6c7086">{{功能增量 N 条 + 验收 M 条 + 管理 K 行 = 总行数 · worktree 各线独立}}</text>
</svg>
