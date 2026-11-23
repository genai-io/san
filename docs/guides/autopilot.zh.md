# Autopilot(副驾)

副驾模型让例行工作持续推进,只在需要你的时候交还控制权。用 `/autopilot` 配置,
用 `shift+tab` 进入(琥珀色的 `⏵⏵ autopilot`),或用 [`/goal`](#goal) 一行启动。

## 面板

```
ALWAYS
▸ [✓] Suggest   your next input · Tab to accept

WHILE IT DRIVES · shift+tab to drive
      Mission   the goal it works toward          not set · Enter to write
      System    how it thinks and decides (system prompt)       built-in
      Model     which model makes the calls              same as session
  [✓] Approve   permission requests · asks you if risky
  [ ] Answer    questions and [y/N] prompts
  [ ] Continue  to the next turn · needs a mission

      Save preset…  reuse this setup in other sessions
      Load preset…  3 saved
```

`enter` 或 `space` 编辑或切换，`←`/`→` 调整 Continue 的上限，
`esc` 保存并关闭。保存不会启动任务。

**Suggest** 在任何模式下都生效：每轮结束后在输入框里用灰字建议下一条输入。
Autopilot 驾驶 mission 时，改为提议朝 mission 推进的下一步。

### Recap

Recap 也适用于任何模式。在 `/reflect` 中按 `Tab` 切换到 **Recap**，用
`↑`/`↓` 选择设置，按 `Enter` 编辑或保存：

| 设置 | 默认值 | 含义 |
|---|---:|---|
| Time away (minutes) | 5 | 终端失去焦点的最短时间（分钟）。`-1` 关闭自动回顾。 |
| Turns between recaps | 3 | 自上次显示回顾以来至少完成的用户轮数（恢复会话后也适用）；首次回顾前从会话开始算起。 |

用户每提交一条提示，主代理正常结束后计为一轮。工具调用、自动续跑、失败或取消的轮次都不计入。未记录轮数的旧会话从下一轮正常完成的用户输入开始计数。
自动回顾不会在达到离开时间阈值前显示；如果回来时仍有一轮在运行，就跳过这次回顾，不会在结束后补发。
自动回顾需要终端上报焦点（tmux 中用 `set -g focus-events on`）；`/recap` 不受这些条件限制。

回顾可能在离开期间提前生成，以复用尚未过期的提示缓存。
生成中显示轻微呼吸的 `※`，完成后显示静止的 `※` 和淡色斜体摘要。

设置保存为 `recap.afterMinutes` 和 `recap.turns`：Git 仓库使用项目设置，否则使用用户设置。
未设置 `recap.afterMinutes` 时，仍沿用旧的 `autoPilot.recapAfterMinutes`。

### While it drives

While it drives 一节只在 Autopilot 模式下生效,模式外整节变暗:

- **Mission** —— 所有判断都朝它靠的目标。
- **System** —— 副驾 system prompt 中可编辑的部分;安全规则固定且始终生效。
- **Model** —— 可选;依次选 provider、模型、thinking 档位。存为 `vendor/model`,
  切换会话模型后仍留在原 provider。
- **Approve** —— 在 Autopilot 模式中默认开启，适用于 Bash、zsh 和 PowerShell。
  需要审批的命令先由副驾判断，但明确要求人工确认的规则和安全检查不变。
  风险较高或判断不清时交还给你。加载 skill 本身是安全的，它运行的命令也遵循这套规则。
- **Answer** —— 回答 `AskUserQuestion` 和命令的 `[y/N]` 提示。
- **Continue** —— 运行中的 mission 每轮结束后发出下一步,直到完成或达到上限
  (默认 20)。

## 执行 mission

只有 **running** 的 mission 会被推进,你自己的消息不会被接着朝 mission 推。

| 状态 | 含义 | 输入框 |
|---|---|---|
| ready | 写好了,未开始 | `Start the mission? enter` |
| running | 副驾在推进 | — |
| paused | 你插话了,或它交还给你 | `Resume the mission? enter` |
| done | 已完成,不会再推进 | — |

你发消息会让运行中的 mission 暂停。修改 mission 或加载预设会回到 ready。恢复会话
时回到 paused。

## /goal

```
/goal add table-driven tests for internal/setting until go test ./... passes
```

直接让 mission 进入 running,打开 Answer 和 Continue 且不限次数,然后开始。
`/goal clear` 撤销。两种结束方式都会让开关回到 `/goal` 之前的样子。

## 配置

```jsonc
{
  "autoPilot": {
    "model": "anthropic/claude-haiku-4-5", // 留空 = 会话模型
    "thinkingEffort": "low",               // 留空 = 模型默认
    "systemPromptFile": "~/prompts/pilot.md",
    "maxContinuations": 20,                // -1 = 不限
    "steers": {
      "suggest": true,     // Suggest
      "permission": true,  // Approve
      "question": true,    // Answer
      "bashPrompt": true,  // Answer
      "turnEnd": true      // Continue
    }
  }
}
```

mission、它的状态和面板里写的 system prompt 按会话保存,不写进这里;预设
(`~/.san/autopilot/<名字>.json`)可以把它们带到别的会话,但不带状态。
