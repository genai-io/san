# Autopilot(副驾)

## 概览

Autopilot 是 San 的自动驾驶系统,旨在最大限度减少人工介入:由一个 copilot
模型对会话进行巡航,让例行工作持续推进,只在真正需要人的时刻交还控制权。
它通过一组可独立启用的介入点(**steer**)行动 —— 提议下一步、放行灰区
工具调用、回答命令的交互问询、回答 `AskUserQuestion`,以及在回合结束后朝
mission 继续推进。默认仅开启灰区权限判定。

用 `shift+tab` 切换到 AutoPilot 模式(循环到琥珀色的 `⏵⏵ autopilot on`),
用 `/autopilot` 面板配置。要免人工启动一个 mission,按面板的 **Start** 按钮
—— 它一步完成「开启 AutoPilot + 提交开场那一步」(见
[启动 mission](#启动-mission))。恢复会话(`san -r <id>`)会回到保存时所在
的模式。

## 六个 steer

Steer 是按需组合的开关,按自主程度从低到高排列。AutoPilot 模式未开启时,
任何 steer 都不会触发。

| Steer | 默认 | 作用 |
|---|---|---|
| **Suggest** | 关 | 把副驾提议的下一步填进输入提示(幽灵文本)—— 有 mission 时朝 mission 提议,没有则退回通用预测。`tab` 接受、`enter` 发送。只建议、不代发。Suggest *关闭*时,AutoPilot 会整体压掉提示,避免副驾怂恿你。 |
| **Permission** | **开** | 自动放行静态规则解不了的灰区工具调用,按可逆性、影响面、数据外泄三轴判断。失败即收紧:任何错误都升级给你。 |
| **Bash** | 关 | 回答已批准命令的交互问询(`Continue? [Y/n]`),仅当回答只是让已批准的动作继续;会扩大范围的一律跳过。 |
| **Skill** | 关 | 直接放行副驾的 skill 加载(不经判官)—— 一个独立的"信任 skill"开关。因为 skill 可能跑脚本,判官往往会把它升级给你;单开这个就能让副驾自动加载 skill,而不必打开整个灰区。关闭时,skill 加载回落到 Permission 判官(或你)。 |
| **Question** | 关 | 当 mission 使选择明确且低风险时替你回答 `AskUserQuestion`,否则留给你。选项标签逐字校验 —— 部分或凭空的回答一律转为留给你。 |
| **End** | 关 | 回合结束后判断是否朝 mission 续跑,并自己敲出下一条指令。受 **Continue at most N times** 约束(默认 20);计数在每次人类回合重置。 |

## Mission(任务)

Mission 是副驾本会话要开往的目标 —— 在 `/autopilot` 面板的 Mission 对话框里
撰写:这是个小编辑器,你打的字就是 mission(`enter` 保存、`alt+enter` 换行、
可粘贴),`ctrl+r` 让副驾就地精炼草稿、`ctrl+c` 清空、`esc` 保存并退出。推进类
steer(Suggest、Question、End)读它;安全类 steer(Permission、Bash)刻意对它
不可见,使动作风险的判断独立于意图。

当 End steer 判定 mission **已完全达成**,会将其退役:清空 mission、steer 归位
到被动基线(Permission + Bash)—— AutoPilot 保持开启,你重新接手,自动放行的
安全网仍在。

## 启动 mission

面板底部一行是两个按钮 —— **Save** 和 **Start**(`←`/`→` 选择、`enter`
执行):

- **Save** 把配置应用到实时会话,并写入 `settings.json` 作为默认种子,但不
  改变模式。只调 steer、或想稍后再用 `shift+tab` 启动时用它。
- **Start** 先做 Save 做的一切,再开启 AutoPilot 并免人工发动 mission:从
  mission 推出开场那一步并自己提交 —— 交代好 mission、按下 Start 就是完整的
  启动。Start 需要一个 mission,没设时它会提示你而不是空转开启。

用 `shift+tab` 落到 AutoPilot 不再自动起步,只会浮出 Suggest steer 的提议
(若开启)。发动 mission 始终是显式的 Start 按钮。

## Demo:一次免人工的脚手架搭建

两分钟跑通完整闭环 —— mission 起步、灰区放行、自动续跑、任务完成 ——
全程不触碰临时目录以外的任何东西。

**1. 在空目录启动 San:**

```bash
mkdir /tmp/autopilot-demo && cd /tmp/autopilot-demo && san
```

**2. 配置 copilot** —— 运行 `/autopilot`:

- 打开 **End**(Permission 默认已开)。
- 打开 **Mission**,交代任务:

  > 搭建一个 `notes/` 目录:`todo.md` 放一个 3 项的清单、`done.md` 留空、
  > `README.md` 说明目录结构。每回合处理一个文件。三个文件齐了之后用
  > `ls notes/` 验证 —— 然后任务即完成。

- `esc` 返回。

**3. 启动巡航** —— 在底部一行按 `→` 选中 **Start**,回车。这是你需要按的
最后一个键:Start 开启 AutoPilot,且在 mission 已设时自己推出开场那一步并
提交。

**4. 观察运行。** 预期的转录大致是:

```
❭ Create notes/todo.md with a 3-item checklist.
  ⎿  autopilot · 1/20
● Write(notes/todo.md)
  ⎿  Write → 5 lines
❭ Create an empty notes/done.md.
  ⎿  autopilot · 2/20
...
● Bash(ls notes/)
  ↳ auto-approved · read-only directory listing
  ⎿  Bash → 3 lines
  ✓ autopilot · mission complete
```

整个运行里的每个 `❭` 都带绿色 `⎿ autopilot` 标记 —— 包括开场那条,全部由
copilot 敲入,你没有碰过输入框。那条 `ls` 是灰区调用,由 Permission steer
就地放行。出现
`✓ mission complete` 时,mission 被清空、steer 归位到被动基线(打开
`/autopilot` 可确认),而 AutoPilot 保持开启。

想体验最轻的一档,只开 **Suggest** 重跑一遍、用 `shift+tab` 启动:copilot 把
每一步以幽灵文本提议在输入框里,你用 `tab` + `enter` 接受发送。

## 读懂转录里的标记

| 标记 | 含义 |
|---|---|
| 绿 `⎿ autopilot · 2/5` | 上方那条 `❭` 是副驾敲的(第 2 / 共 5 次续跑) |
| 绿 `↳ auto-approved · <理由>` | 判官放行了上方的工具调用 |
| 琥珀 `↳ escalated · <理由>` | 判官把调用退回给你 |
| 绿 `⏵ autopilot · answered for you` | 副驾替你回答了 `AskUserQuestion` |
| 琥珀 `↩ autopilot · this question is yours` | 它把问题留给了你 |
| 琥珀 `↩ autopilot · over to you` | 它停手并交还控制权(判定出错时错误原因缀在后面) |
| 绿 `✓ autopilot · mission complete` | mission 完成并退役 |

判定进行中,模式行显示 `⏵⏵ autopilot · thinking…`;放行计数也在那里
(`· 3 approved · 1 escalated`)。

## 配置

面板编辑的是本会话的实时配置。model、steer、续跑上限保存进 `settings.json`,作为
新会话的默认值。**system prompt** 和 **mission** 则按会话走:它们随转录持久化、
`/resume` 时恢复,但不会被写成默认值 —— 新会话从内置 prompt、无 mission 起步。要把
自定义的 prompt 或 mission 带到另一个会话,导出成预设再在那边导入。

```jsonc
{
  "autoPilot": {
    "model": "anthropic/claude-haiku-4-5", // steer 判定用的模型;留空用会话模型
    "systemPrompt": "…",                   // “怎么开”;按会话走,面板不会写到这里
    "systemPromptFile": "~/prompts/pilot.md", // 持久的自定义默认;systemPrompt 为空时生效
    "mission": "…",                        // 按会话;在面板里设置
    "maxContinuations": 20,
    "steers": {
      "suggest": true,
      "permission": true,  // 省略即默认开;false 则一律升级给你
      "bashPrompt": true,  // Bash steer
      "skill": true,       // Skill steer —— 信任 skill 加载
      "question": true,
      "turnEnd": true      // End steer
    }
  }
}
```

命名预设打包整份副驾配置 —— system prompt、mission 和 steer。在 `/autopilot`
菜单里,`e` 导出当前配置、`i` 导入,存取于 `~/.san/autopilot/<name>.json`。

## 关联

- [权限模型](../concepts/permission-model.md) —— Permission steer 判定的灰区
  来自这套静态规则;被硬性拦截的动作永远到不了判官面前。
- 判官组件在 `internal/reviewer`(`reviewer.Judge`);steer 与面板在
  `internal/app` / `internal/app/input`。
