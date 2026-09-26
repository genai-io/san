# Autopilot(副驾)

副驾模型让例行工作持续推进,只在需要你的时候交还控制权。用 `/autopilot` 配置,
用 `shift+tab` 进入(琥珀色的 `⏵⏵ autopilot`),或用 [`/goal`](#goal) 一行启动。

## 面板

```
GIVE IT
▸ Mission    the goal it works toward                      not set · Enter to write
  System     how it thinks and decides (system prompt)     built-in
  Model      which model makes the calls                   same as session
LET IT
  [✓] Suggest   your next input · Tab to accept
  [✓] Approve   permission requests · asks you if risky
  [ ] Answer    questions and [y/N] prompts
  [ ] Continue  to the next turn · needs a mission
PRESETS
  Save preset…  reuse this setup in other sessions
  Load preset…  3 saved
```

`enter` 或 `space` 编辑或切换,`←`/`→` 调整 Continue 的上限,`esc` 保存并关闭。
保存不会启动任务。

- **Mission** —— 所有判断都朝它靠的目标。
- **System** —— 副驾 system prompt 中可编辑的部分;安全规则固定且始终生效。
- **Model** —— 可选;依次选 provider、模型、thinking 档位。存为 `vendor/model`,
  切换会话模型后仍留在原 provider。
- **Suggest** —— 在输入框里用灰字建议下一条输入,任何模式都生效。
- **Approve** —— 审查权限请求;有风险或判断失败的交给你。加载 skill 是安全的,
  它运行的脚本按 Bash 调用审查。
- **Answer** —— 回答 `AskUserQuestion` 和命令的 `[y/N]` 提示。
- **Continue** —— 运行中的 mission 每轮结束后发出下一步,直到完成或达到上限
  (默认 20)。

Approve、Answer、Continue 只在 Autopilot 模式下生效。

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
