# Autopilot(副驾)

## 概览

Autopilot 是 San 的自动驾驶系统,旨在最大限度减少人工介入:由一个 copilot
模型对会话进行巡航,让例行工作持续推进,只在真正需要人的时刻交还控制权。
你给它一个 mission、一段 system prompt 和一个模型,再允许它替你做四件事:
建议你的下一条输入、批准权限请求、回答问题,以及一轮接一轮地朝 mission 推进。
Suggest 和 Approve 默认开启。

用 `/autopilot` 面板配置,再用 `shift+tab` 进入 Autopilot 模式(循环到琥珀色的
`⏵⏵ autopilot`)。恢复会话(`san -r <id>`)会回到保存时所在的模式。只想先看它
跑起来的话,[`/goal`](#goal) 是最短的入口。

## 面板

`/autopilot` 是一页,分三组:

```
✦ Autopilot
GIVE IT
▸ Mission    the goal it works toward                      not set · Space to write
  System     how it thinks and decides (system prompt)     built-in
  Model      which model makes the calls                   same as session

LET IT
  [✓] Suggest   your next input · Tab to accept
  [✓] Approve   permission requests · asks you if risky
  [ ] Answer    questions and [y/N] prompts
  [ ] Continue  to the next turn · needs a mission

PRESETS
  Save preset…   reuse this setup in other sessions
  Load preset…    3 saved

↑↓ navigate · Space edit/toggle · ←→ adjust · Enter save · Esc discard
```

`space` 操作当前行(打开编辑器或切换开关),`←`/`→` 调整 Continue 的上限,
`enter` 保存,`esc` 放弃。保存会把改动应用到当前会话,并写进 `settings.json`
作为新会话的默认值;保存本身不会启动任务 —— 见[执行 mission](#执行-mission)。

### Give it(给它什么)

- **Mission** —— 它要达成的目标。所有判断都朝它靠:它建议或发出的下一步,以及
  Approve、Answer 权衡时参考的意图。编辑器里整段文字就是 mission(`alt+enter`
  换行、可粘贴),`ctrl+r` 让副驾就地精炼草稿,`ctrl+c` 清空,`enter` 或 `esc`
  返回。这一行右侧显示 mission 当前的状态(`ready`、`running`、`paused`、`✓ done`)。
- **System** —— 它怎么思考和决策:副驾 system prompt 里可编辑的那部分,初始为
  内置指令。安全规则是另一份固定的策略,每次判断都会带上,这里改不到;这里只改
  它的行事方式。按会话生效。
- **Model** —— 用哪个模型来做判断。可选:"same as session" 跟随会话模型。否则
  先选已连接的 provider,再选它缓存的模型;如果模型支持推理,再选 thinking 档位
  (`default` 用模型自己的默认档)。保存为 `vendor/model`,所以切换会话模型后
  副驾仍留在原 provider。一般用便宜、快速的模型就够了;显式设了档位时会取消
  512 token 的判定上限,免得推理吃掉答案。

### Let it(让它做什么)

除 Suggest 外,其余开关只在 Autopilot 模式下生效;Suggest 在任何模式下都控制
输入提示。

| 开关 | 默认 | 作用 |
|---|---|---|
| **Suggest** your next input | **开** | 在输入框里以灰字显示下一条输入建议;`tab` 采纳,`enter` 发送,它不会自行提交。有 mission 时建议朝 mission 的下一步,否则预测你想输入什么。 |
| **Approve** permission requests | **开** | 审查静态规则交给你决定的工具调用,按可逆性、影响面、数据外泄三方面判断。在 git 下以版本历史为安全网:改已跟踪文件属于常规操作,git 自身那些危险操作(`reset --hard`、`clean -f`、force-push 等)按 mission 权衡而不是一律拦下。出了工作区的仍然问你;出错即收紧,任何错误都转给你。加载 skill 视为读取说明,skill 指向的脚本在真正运行时才会被审查。 |
| **Answer** questions and [y/N] prompts | 关 | 当 mission 或对话足以支撑一个合理选择时,替你回答 AI 的 `AskUserQuestion`;当回复只是让已批准的命令继续时,替你回应命令的交互提示(`Continue? [Y/n]`)。真正该你拍板的留给你,会扩大范围的提示一律跳过。 |
| **Continue** to the next turn | 关 | 运行中的 mission 每一轮结束后,判断是否已完成,没完成就自己发出下一步。需要 mission。受上限约束(`←`/`→`:5、10、20、50、100 或不限;默认 20)。 |

## 执行 mission

Mission 有状态,只有 **running** 状态的 mission 会被推进。这样 mission 的轮次和
你自己的对话是分开的:Continue 不会把 mission 接在你发起的对话后面。

| 状态 | 含义 | Continue 是否推进 | 输入框提示 |
|---|---|---|---|
| — | 没有 mission | 否 | — |
| **ready** | 写好了,还没开始 | 否 | `Start the mission? enter to start · esc to skip` |
| **running** | 副驾正在推进 | **是** | — |
| **paused** | 你插话了,或副驾交还给你 | 否 | `Resume the mission? enter to resume · esc to skip` |
| **done** | 副驾判断已完成 | 否 | — |

- **开始。** 在面板里保存 mission,再用 `shift+tab` 进入 Autopilot:输入框会询问
  是否开始,按 `enter` 即开始。副驾推出第一步并发出。已经在 Autopilot 里的话,
  保存后询问会立刻出现。
- **你自己的消息。** 一打字,询问就让开,`enter` 发送的是你输入的内容。这一轮属于
  你:运行中的 mission 会暂停,你这一轮结束后输入框会询问是否继续。你输入的内容
  不会被当成 mission 的一步。
- **交还。** 副驾需要只有你能做的决定、这一轮被中断(`esc`、stop hook、离开
  Autopilot)或达到上限时,mission 暂停。继续时计数接着算,如果是上限用完则重新
  开始计数。
- **完成。** 副驾判断 mission 已完成时把它标记为 done:保留记录,但不会再被推进,
  也不再影响之后的判断。你的开关保持原样。要做新任务,写一个新的 mission。
- **修改 mission** —— 改写它,或加载带 mission 的预设 —— 会让它回到 ready,
  重新开始一次执行。
- **恢复会话**时,运行中的 mission 会变成 paused,在你确认之前不会自己跑。

## /goal

常见用法 —— 交代 mission、打开让副驾行动的开关、取消上限、启动 —— 可以压缩成一行:

```
/goal add table-driven tests for internal/setting until go test ./... passes
```

- 目标成为 [mission](#执行-mission),直接进入 running
- Answer 和 Continue 打开
- 取消上限 —— 目标达成才结束,而不是计数用完
- 进入 Autopilot,副驾自己发出第一步

在一轮进行中下达的话,会在当前这一轮结束后接手。单独输入 `/goal` 显示当前目标;
`/goal clear` 撤销它。

它刻意只作用于本会话:不同于面板的保存,它不会改写你保存的默认配置。**Approve**
保持你原来的设置不变,因为显式关掉它是一个安全选择,目标无权推翻。目标达成或被
撤销后,开关回到 `/goal` 接手前的样子。

想要别的组合就用面板:只建议不提交、限定续跑次数,或者自定义 system prompt。

## 保持自主

Continue 开着时,运行中的 mission 会被推着越过那些原本会让会话停下等你的情况:

- **中途停下的一轮** —— 达到步数上限,或输出被截断且无法恢复 —— 会被接着做,副驾
  会知道它是怎么停的。你自己按 `esc` 不一样:取消一轮代表你接手,mission 暂停。
  stop hook 也一样。
- **直接失败的一轮** 会按递增的间隔(5s、10s、15s)等待,然后判断是否继续,最多
  连续三次;任何一轮正常结束都会重置计数。需要你处理的错误仍然交还给你。
- **副驾调用失败**(网络抖动、回复不是 JSON)会重试最多三次,不会因此结束 mission。
- **判断进行中遇到上下文压缩** 会先保留结论,而不是丢掉。
- **续跑次数用完**:把 Continue 的上限设为不限,mission 完成才结束,而不是计数
  用完就停。这时唯一的停止条件是副驾自己的判断,所以 mission 里要写清楚完成标准。

不限次数的运行适合搭配快速、便宜的模型 —— 见 [Model](#give-it给它什么)。

## 演示:免手动搭建一个目录

一次约两分钟的运行,走完整个流程 —— 开场、权限审批、续跑、完成 —— 只在一个临时
目录里操作。

**1. 在空仓库里启动 San。** 只在这里运行 —— 下面的目标会在启动目录写 `notes/`,
放到你自己的项目里就会写进真实目录:

```bash
mkdir /tmp/autopilot-demo && cd /tmp/autopilot-demo && git init -q && san
```

`git init` 不是可有可无 —— 在 git 下,Approve 会把改已跟踪文件视为可恢复,这正是
让这次运行不停下来问你的原因。

**2. 下达目标。** 一行,也是你按的最后一个键:

```
/goal 搭建一个 notes/ 目录:todo.md 放一个 3 项的清单、done.md 留空、
README.md 说明目录结构。每回合只处理一个文件。三个文件齐了之后用
ls notes/ 验证 —— 然后目标即达成。
```

这段话里有三个细节在起作用:*每回合只处理一个文件* 逼出多次续跑,好让你看清;
*ls notes/* 在路径上放了一个需要审查的 bash 调用;*然后目标即达成* 给了副驾一个
它真能验证的完成条件。

**3. 观察运行。** 预期的转录大致是:

```
⏵ autopilot · goal set

❭ Create notes/todo.md with a 3-item checklist.
  ⎿  autopilot · step 1
● Write(notes/todo.md)
  ⎿  Write → 5 lines

❭ Create an empty notes/done.md.
  ⎿  autopilot · step 2
...
● Bash(ls notes/)
  ↳ auto-approved · read-only directory listing
  ⎿  Bash → 3 lines

✓ autopilot · mission complete
```

每个 `❭` 都带绿色 `⎿ autopilot` 标记 —— 包括开场那条,全部由副驾输入,你没有碰过
输入框。那条 `ls` 由 Approve 就地放行。出现 `✓ mission complete` 时,mission 显示
为 done,开关回到 `/goal` 接手前的样子(打开 `/autopilot` 可确认),Autopilot
保持开启。想中途停下就 `/goal clear`。

同一轮用面板走:把上面那段话写成 **Mission**,打开 **Continue**,按 `enter` 保存,
再 `shift+tab` 进入 Autopilot,在开始询问上按 `enter`。想体验最轻的一档,只开
**Suggest**:副驾把每一步以灰字建议在输入框里,你用 `tab` + `enter` 采纳发送。

## 读懂转录里的标记

| 标记 | 含义 |
|---|---|
| 绿色 `⎿ autopilot · 2/5` | 上面那条 `❭` 是副驾输入的(第 2 次续跑,共 5 次;不限次数时显示 `step 2`) |
| 琥珀色 `⏵ autopilot · turn failed · retrying in 5s` | 这一轮出错了,副驾会判断是否继续 |
| 绿色 `↳ auto-approved · <原因>` | Approve 放行了上面那个工具调用 |
| 琥珀色 `↳ escalated · <原因>` | Approve 把调用转给了你 |
| 绿色 `⏵ autopilot · answered for you` | 副驾替你回答了 `AskUserQuestion` |
| 琥珀色 `↩ autopilot · this question is yours` | 它把问题留给了你 |
| 琥珀色 `↩ autopilot · over to you` | 它停下并交还控制权,mission 暂停(判断出错时后面附带错误信息) |
| 绿色 `✓ autopilot · mission complete` | mission 已完成 |

判断进行中时,模式行显示 `⏵⏵ autopilot · thinking…`;审批数量也统计在这里
(`· 3 approved · 1 escalated`)。

## 配置

模型、开关和 Continue 的上限会保存进 `settings.json`,作为新会话的默认值。mission、
它的状态和 system prompt 按会话走:随转录保存、`/resume` 时恢复,但不会写成默认值。
要把 mission 或自定义 system prompt 带到另一个会话,保存成预设再在那边加载。

System prompt 决定副驾的行事方式,但替代不了固定的控制策略。每次副驾判断都会带上
那份策略,它规定了信任边界、出错即收紧的行为、各项任务的安全规则和输出格式。
`systemPrompt` / `systemPromptFile` 只提供可编辑的那部分。

```jsonc
{
  "autoPilot": {
    "model": "anthropic/claude-haiku-4-5", // 留空 = 会话模型
    "thinkingEffort": "low",               // 留空 = 模型默认档位
    "systemPrompt": "…",                   // 按会话;面板不会写到这里
    "systemPromptFile": "~/prompts/pilot.md", // 持久默认值;systemPrompt 为空时使用
    "maxContinuations": 20,                // Continue 的上限;-1 = 不限
    "steers": {
      "suggest": true,     // Suggest
      "permission": true,  // Approve;省略即默认(开)
      "question": true,    // Answer —— AI 的提问
      "bashPrompt": true,  // Answer —— 命令的 [y/N] 提示
      "turnEnd": true      // Continue
    }
  }
}
```

面板里的 Answer 开关同时设置 `question` 和 `bashPrompt`。这些 key 保留原来的名字
以保持兼容;遗留的 `"skill"` key 会被忽略 —— skill 加载和其他调用一样走 Approve。

预设把整套配置 —— mission 文字、system prompt、模型和开关 —— 存到
`~/.san/autopilot/<名字>.json`。预设是模板:它不记录某次执行进行到哪里,所以
加载进来的 mission 一律从 ready 开始。

## 与其他功能的关系

- [权限模型](../concepts/permission-model.md) —— 静态规则留下的灰区由 Approve
  审查;硬拦截的动作根本到不了它。
- 判官组件在 `internal/reviewer`(`reviewer.Judge`);mission 生命周期和开关在
  `internal/app`,面板在 `internal/app/input`。
