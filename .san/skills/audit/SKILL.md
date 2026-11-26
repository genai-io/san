---
name: audit
description: 审查 San 的模型上下文和工程质量。默认审查两项，也可指定其中一项。
argument-hint: "[context|engineering] [范围或关注点]"
---

# 审查 San

审查只读，不改代码。上下文审查默认用中文报告；工程审查按工程指南用简明英语报告。方向之外的参数表示审查范围或关注点。

- `/audit`：执行两项审查，按「上下文」和「工程质量」分节报告。
- `/audit context`：只按[上下文指南](references/context.md)审查 San 向模型发送了什么，以及变化如何影响任务表现、缓存和分支开销。
- `/audit engineering`：只按[工程指南](references/engineering.md)按指定范围（默认全仓）的预期负载，审查正确性、安全与数据、并发与规模、测试、性能、精简和可维护性。
