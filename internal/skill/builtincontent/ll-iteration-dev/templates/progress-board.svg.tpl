<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="480" font-family="Microsoft YaHei, sans-serif">
  <rect width="1000" height="480" fill="#1e1e2e"/>
  <text x="500" y="32" text-anchor="middle" font-size="18" fill="#cdd6f4">并行开发进展板 — {{YYYY-MM-DD HH:MM}}</text>

  <!-- 批次总览（2 块并排，可增删；460×110） -->
  <rect x="30" y="55" width="460" height="110" rx="10" fill="#313244" stroke="#a6e3a1" stroke-width="2"/>
  <text x="50" y="82" font-size="14" fill="#a6e3a1">{{✅ 批次名 — 状态}}</text>
  <text x="50" y="106" font-size="11" fill="#a6adc8">{{关键数据 1（如 N 任务 verified）}}</text>
  <text x="50" y="126" font-size="11" fill="#a6adc8">{{关键数据 2}}</text>
  <text x="50" y="146" font-size="11" fill="#a6adc8">{{关键数据 3}}</text>
  <rect x="510" y="55" width="460" height="110" rx="10" fill="#313244" stroke="#f9e2af" stroke-width="2"/>
  <text x="530" y="82" font-size="14" fill="#f9e2af">{{🟡 下一批 — 状态}}</text>
  <text x="530" y="106" font-size="11" fill="#a6adc8">{{摘要 1}}</text>

  <!-- 在途开发线（卡片行：y=228 起步长 60，宽 900 高 52） -->
  <rect x="30" y="185" width="940" height="170" rx="10" fill="#313244" stroke="#89b4fa" stroke-width="2"/>
  <text x="50" y="212" font-size="14" fill="#89b4fa">🔨 在途开发线（{{N 会话 M 件}}）</text>
  <rect x="50" y="228" width="900" height="52" rx="6" fill="#45475a"/>
  <text x="65" y="250" font-size="13" fill="#f9e2af">{{会话名 · 进行中 🟡}}</text>
  <text x="65" y="270" font-size="11" fill="#a6adc8">{{任务件摘要 · hash 链}}</text>

  <!-- 资源位（审计/待命） -->
  <rect x="30" y="375" width="940" height="75" rx="10" fill="#313244" stroke="#cba6f7" stroke-width="2"/>
  <text x="50" y="402" font-size="14" fill="#cba6f7">🔍 审计资源（{{待命 ×N}}）</text>
  <text x="50" y="426" font-size="11" fill="#a6adc8">{{会话名 💤 · 分工}}</text>
</svg>
