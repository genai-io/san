# 提案：身份体系重构 — 基于目录的 Prompt 配置

## 摘要

将 Identity 重新设计为 **目录结构**，分别位于 `~/.gen/identities/`（用户级）
和 `.gen/identities/`（项目级）。每个身份是一个目录，包含：
- `identity.md` — 角色配置（frontmatter + 前言），最常见的情况
- `prompts/` — **可选的** 角色层 prompt 覆盖
- `skills/` — **可选的** 捆绑技能，仅在此身份激活时生效

**核心机制：提取的默认值 + 基于文件的回退。** 默认 prompt 内容通过
`//go:embed` 编译到二进制中，并在启动时**提取到磁盘上的 `default/prompts/`**。
这些文件作为用户创建自定义身份时的**参考**。每次启动时，系统会验证其完整性——
如果用户修改了它们，它们会被**覆盖**为规范版本。

自定义身份只需要包含与默认值的**差异部分**。
当某个身份缺少 prompt 文件时，系统回退到磁盘上的 `default/prompts/`。

首个新内置身份：**`readonly`**，具有最小化的 prompt 和只读工具。

**锁定层。** 策略由框架强制执行，**永远不可移除**——
它不在 `sections:` 中列出，始终由框架注入。

## `sections:` 与 `prompts/` —— 谁控制什么

组装系统 prompt 时有两个关注点：

1. 加载**哪些**节，以及以什么**顺序**
2. 每个节使用**什么内容**

这两者是分开的，设计上也显式地分开：

| 关注点 | 由谁控制 | 描述 |
|---|---|---|
| 哪些节 + 顺序 | `identity.md` 中的 `sections:` | **唯一真相来源。** 此身份加载的角色层节的完整声明式列表，按顺序排列。 |
| 每个节的内容 | `prompts/` 目录 | **覆盖机制。** 此处的文件为 `sections:` 中声明的节提供自定义内容。 |

### 规则

1. **`sections:` 是完整的清单。** 只有列在 `sections:` 中的节才会被加载。
2. **`prompts/` 只影响已在 `sections:` 中的节。** `prompts/` 中属于未在 `sections:` 中声明的节的文件会被**忽略**（并产生警告）。
3. **`sections:` 中每个节的内容解析：**
   - 查找 `<identity>/prompts/<section>.txt` → 存在？使用它
   - 不存在？→ 回退到 `default/prompts/<section>.txt`
   - 两者都不存在？→ 该节为空，不注入
4. **Policy 永远不在 `sections:` 中** — 它始终由框架在最后注入。

### 为什么不用 `prompts/` 目录列表作为节列表？

因为这样就无法表达"从默认值加载此节，但不需要为它创建文件"。每个身份
都需要一份它想加载的每个节的完整副本。而且 `readonly` 无法表达"只加载
environment"而不删除文件——但它根本没有文件可删，因为它使用的是回退。

有了 `sections:` 作为显式列表，`readonly` 只需声明 `[environment]` 就能
精确得到它想要的内容。自定义身份声明它想要的完整集合，只为有所不同的节
创建覆盖文件。

### 示例

```
# code-reviewer：加载 4 个节，覆盖 1 个
sections: [output, engineering, guidelines, environment]
prompts/
  └── output.txt          ← 自定义 output 内容

# readonly：加载 1 个节，无覆盖
sections: [environment]
prompts/                  ← 空或不存在；environment 回退到默认值

# concise：加载 4 个节，覆盖 2 个
sections: [output, engineering, guidelines, environment]
prompts/
  ├── output.txt          ← 自定义 output 内容
  └── engineering.txt     ← 自定义 engineering 内容
  # guidelines、environment → 回退到默认值

# 反模式：guidelines.txt 存在但 guidelines 不在 sections 中
sections: [output, engineering, environment]
prompts/
  └── guidelines.txt      ← 被忽略（带警告）：guidelines 不在 sections: 中
```

## 目录结构

```
~/.gen/identities/
│
├── default/                        ← 默认身份（内置，初始化时写入）
│   ├── identity.md                 ← frontmatter + 前言
│   └── prompts/                    ← 完整的默认 prompt 集合（参考用）
│       ├── output.txt              ← 从二进制中提取；请勿修改
│       ├── engineering.txt         ← 从二进制中提取；请勿修改
│       ├── guidelines.txt          ← 从二进制中提取；请勿修改
│       └── environment.txt         ← 从二进制中提取；请勿修改
│
├── readonly/                       ← 只读身份（内置，初始化时写入）
│   ├── identity.md
│   └── prompts/                    ← 大部分为空，全部回退到默认值
│       └── environment.txt         ← 可选；也可以省略（回退）
│
└── code-reviewer/                  ← 用户自定义身份，带覆盖
    ├── identity.md                 ← frontmatter + 前言
    ├── prompts/                    ← 可选的角色层覆盖
    │   └── output.txt              ← 自定义 output；其余回退到默认值
    └── skills/                     ← 可选的捆绑技能
        └── lint-rules/
            └── SKILL.md

.gen/identities/                    ← 项目级（名称冲突时覆盖用户级）
└── project-role/
    └── identity.md
```

### 与当前结构的对比

```
当前:                                    新:
~/.gen/identities/                      ~/.gen/identities/
├── README.md                           ├── default/
└── ml-engineer.md    ← 单个 .md 文件    │   ├── identity.md       ← frontmatter + 前言
                                        │   └── prompts/          ← 供用户参考的副本
                                        │       ├── output.txt
                                        │       ├── engineering.txt
                                        │       ├── guidelines.txt
                                        │       └── environment.txt
                                        ├── readonly/
                                        │   ├── identity.md
                                        │   └── prompts/
                                        │       └── （空，全部回退）
                                        ├── code-reviewer/
                                        │   ├── identity.md
                                        │   ├── prompts/
                                        │   │   └── output.txt
                                        │   └── skills/
                                        │       └── lint-rules/SKILL.md
                                        └── old-style.md          ← 已迁移，正文→前言
```

## 为什么要把默认值放到磁盘上？

将默认值提取到 `default/prompts/` 有一个关键目的：
**用户需要一个参考来了解可以自定义什么。** 在创建自定义身份时，
用户可以查看 `default/prompts/output.txt` 来了解当前的默认输出 prompt，
然后决定要修改什么。

没有这个，用户只能猜测有哪些节、默认内容是什么样的、以及使用什么格式。
磁盘副本是用户可以阅读的真相来源——但不能修改。

### 完整性强制

**每次启动时**，系统验证 `default/prompts/` 的完整性：

```go
func verifyDefaultPrompts() error {
    for _, filename := range builtin.PromptFiles {
        diskPath := filepath.Join(defaultIdentityDir, "prompts", filename)
        canonical := builtin.ReadPrompt(filename)
        onDisk, err := os.ReadFile(diskPath)

        if os.IsNotExist(err) {
            // 缺失 — 恢复
            os.WriteFile(diskPath, canonical, 0644)
            continue
        }

        if bytes.Equal(onDisk, canonical) {
            continue // 未更改，OK
        }

        // 用户修改了文件 — 用规范版本覆盖
        log.Warn("default prompt modified, restoring: %s", filename)
        os.WriteFile(diskPath, canonical, 0644)
    }
    return nil
}
```

关键行为：
- **每次启动**：运行完整性检查，而不仅仅在 init/upgrade 时
- **被修改？覆盖。** 用户对 `default/prompts/` 的更改会被还原
- **缺失？恢复。** 被删除的文件会被重新提取
- **用户创建的身份永远不会被触及** — 仅 `default/prompts/`
- **Policy 不在 `default/prompts/` 中** — 它由框架强制执行，由系统直接注入

想要自定义 prompt 的用户应该创建自己的身份目录，而不是修改 `default/prompts/`。

## 身份解析：全局 + 项目

`identity/registry.go` 目前已同时解析两个作用域。保留两者——项目级在名称
冲突时优先：

```
/identity foo

  1. .gen/identities/foo/     → 找到？使用项目级
  2. ~/.gen/identities/foo/   → 找到？使用用户级
  3. 两者都没有                → 使用默认身份
```

这保留了现有行为，即项目级配置可以覆盖用户级配置。

## 内容解析

对于 `sections:` 中声明的每个节，按以下方式解析内容：

```
对于身份 "code-reviewer"，sections: [output, engineering, guidelines, environment]

  output:
    1. code-reviewer/prompts/output.txt     → 存在！使用它（自定义）

  engineering:
    1. code-reviewer/prompts/engineering.txt → 不存在
    2. default/prompts/engineering.txt       → 存在！使用它（回退）

  guidelines:
    1. code-reviewer/prompts/guidelines.txt  → 不存在
    2. default/prompts/guidelines.txt        → 存在！使用它（回退）

  environment:
    1. code-reviewer/prompts/environment.txt → 不存在
    2. default/prompts/environment.txt       → 存在！使用它（回退）

对于身份 "readonly"，sections: [environment]

  environment:
    1. readonly/prompts/environment.txt      → 不存在
    2. default/prompts/environment.txt       → 存在！使用它（回退）

  注意：output、engineering、guidelines 不在 sections: 中 → 完全不加载。
  即使 readonly/prompts/output.txt 存在，也会被忽略。
```

```go
func resolveSections(identity *Identity) []ResolvedSection {
    var resolved []ResolvedSection

    for _, sectionName := range identity.Sections {
        content := ""

        // 1. 查找身份自己的覆盖文件
        p := filepath.Join(identity.Dir, "prompts", sectionName+".txt")
        if c, ok := readFile(p); ok {
            content = c
        } else {
            // 2. 回退到默认身份在磁盘上的同名文件
            p = filepath.Join(defaultIdentityDir, "prompts", sectionName+".txt")
            if c, ok := readFile(p); ok {
                content = c
            }
        }

        if content != "" {
            resolved = append(resolved, ResolvedSection{
                Name:    sectionName,
                Content: content,
            })
        }
    }

    // 3. 对 prompts/ 中未在 sections: 中声明的孤儿文件进行警告
    warnOrphanFiles(identity.Dir, identity.Sections)

    return resolved
}

// warnOrphanFiles 检查 prompts/ 中不在 sections: 中的文件并警告用户。
// 这些文件会被忽略。
func warnOrphanFiles(identityDir string, declaredSections []string) {
    promptsDir := filepath.Join(identityDir, "prompts")
    entries, _ := os.ReadDir(promptsDir)
    declared := setOf(declaredSections)
    for _, e := range entries {
        name := strings.TrimSuffix(e.Name(), ".txt")
        if !declared[name] {
            log.Warn("identity %s: prompts/%s ignored — section \"%s\" not declared in sections:",
                filepath.Base(identityDir), e.Name(), name)
        }
    }
}
```

## 锁定层 vs 角色层

身份更改的范围限定在**角色层**。策略由框架强制执行，不可移除：

```
┌─────────────────────────────────────────────┐
│ 身份可以更改（角色层）                       │
│                                             │
│  • 前言（身份声明）                          │
│  • output / engineering 风格                 │
│  • guidelines（工具使用、提醒等）              │
│  • environment（日期、cwd、平台）             │
│  • 工具权限（与现有权限层集成）                │
│  • 捆绑技能                                  │
└─────────────────────────────────────────────┘

┌─────────────────────────────────────────────┐
│ 框架强制执行 — 永远不可移除                   │
│                                             │
│  • policy（安全契约）                         │
└─────────────────────────────────────────────┘
```

这意味着：
- 身份配置中的 `sections:` 是**完整的清单**，列出要加载的角色层节及其顺序
- `prompts/` 文件只影响已在 `sections:` 中声明的节
- `policy` 永远不会出现在 `sections:` 中——它始终由框架在角色节之后注入
- 身份不能意外（或故意）移除安全契约
- **所有**角色节的默认内容都在磁盘上 `default/prompts/` 中供参考

## identity.md 格式

单个文件，YAML frontmatter + markdown 正文。正文即为前言。

**`sections:` 是必填项。** 它是此身份加载哪些节以及以什么顺序加载的
完整声明式列表。

### default/identity.md

```markdown
---
name: default
description: 内置 Gen Code 角色 — 软件工程通才
sections:
  - output
  - engineering
  - guidelines
  - environment
---

你是 Gen Code，一个在终端中运行、用于软件工程任务的交互式 AI 助手。
```

### readonly/identity.md

```markdown
---
name: readonly
description: 只读助手 — 搜索、分析、回答问题
sections:
  - environment
---

你是一个 AI 助手。你可以读取文件、搜索代码和回答问题。你不能修改文件
或执行命令。
```

### code-reviewer/identity.md（用户自定义，带覆盖）

```markdown
---
name: code-reviewer
description: 代码审查专家 — 只读，专注于逻辑和风格
sections:
  - output
  - engineering
  - guidelines
  - environment
---

你是一名代码审查专家。仔细阅读代码，找出 bug、安全漏洞、性能问题和风格
不一致之处。
```

配合 `code-reviewer/prompts/output.txt`：

```
<output>
审查要彻底。引用文件路径和行号。提出修复建议。
</output>
```

注意：`output` **必须在 `sections:` 中声明**，`prompts/output.txt` 才能生效。
如果 `output` 被从 `sections:` 中省略，该文件将被忽略并产生警告。

其他节（`engineering`、`guidelines`、`environment`）在 `sections:` 中但
没有覆盖文件 → 回退到 `default/prompts/`。

Policy 始终由框架注入。

### my-custom/identity.md（用户自定义，覆盖 output）

```markdown
---
name: my-custom
description: 我的自定义角色 — 简洁输出 + 工程标准
sections:
  - output
  - engineering
  - guidelines
  - environment
---

你是一个简洁的编程助手。每次回复最多一句话。
```

配合 `my-custom/prompts/output.txt`：

```
<output>
始终保持简洁。使用要点列表。永远不要道歉。
</output>
```

## 捆绑技能

Skills 已经是基于目录的，由 `skill/loader.go` 加载，具有多个搜索作用域。
当前激活身份的 `skills/` 目录被添加为一个额外的搜索根——仅在身份激活期间
生效：

```
技能加载器搜索路径（当身份 "code-reviewer" 激活时）：

  1. identities/code-reviewer/skills/   ← 新增：身份捆绑的技能，仅在此身份激活时生效
  2. ~/.gen/skills/                     ← 用户安装的技能
  3. .gen/skills/                       ← 项目技能
  4. built-in skills                    ← 随二进制一起提供的技能
```

这使得身份变得自包含且可分享：

```bash
# 导出一个身份及其技能
tar -czf code-reviewer.tar.gz -C ~/.gen/identities code-reviewer/

# 导入 — 一切随之而来
tar -xzf code-reviewer.tar.gz -C ~/.gen/identities/
gen> /identity code-reviewer
```

待定选择：**捆绑**技能（自包含，可作为 tarball 分享）vs **按名称引用**
（`skills: [git:commit]`，无重复）。默认使用捆绑，同时允许引用。

## 工具权限

工具通过**现有权限层**进行作用域控制，而不是并行 `tools: allow/deny` 系统。
身份的工具约束被合并到当前权限配置中：

```markdown
---
name: readonly
description: 只读助手
sections:
  - environment
tools:
  allow:
    - Read
    - Grep
    - Glob
    - WebSearch
    - WebFetch
---
```

- `tools:` 为空或省略 → 无限制（所有工具可用）
- `tools.allow:` → 仅这些工具可用
- 工具权限与现有权限层进行**交集运算**
  （例如，如果框架已禁止 `Bash`，身份无法重新启用它）

## 迁移：扁平 `<name>.md` → 目录

现有的扁平 `~/.gen/identities/<name>.md` 文件以非破坏性方式迁移：

```
迁移前:                               迁移后:
~/.gen/identities/                     ~/.gen/identities/
└── ml-engineer.md                     └── ml-engineer/
    (frontmatter: name, sections           └── identity.md
     body: 前言)                              (同一文件，body → 前言)
```

迁移逻辑：
1. 扫描 `~/.gen/identities/` 中不在子目录内的 `.md` 文件
2. 对每个文件：创建 `<name>/` 目录，将文件移动到 `<name>/identity.md`
3. 原始 `.md` 的正文变成前言（`identity.md` 的 markdown 正文）

此操作在升级后的首次启动时自动运行。

## 初始化

在 `gen init` 或首次运行时，内置身份内容被提取到
`~/.gen/identities/`。每次启动时，验证完整性：

```go
func Initialize(cwd string) {
    // 将扁平 .md 文件迁移到目录结构（一次性）
    migrateFlatIdentityFiles()

    // 将内置身份文件提取到磁盘（如果缺失）
    ensureIdentityDir("default", builtin.DefaultConfig, builtin.DefaultPrompts)
    ensureIdentityDir("readonly", builtin.ReadonlyConfig, builtin.ReadonlyPrompts)

    // 验证默认 prompt 的完整性（每次启动）
    verifyDefaultPrompts()
}

// ensureIdentityDir 写入 identity.md 和 prompts/ 目录。
// 所有身份：已存在的文件不会被覆盖（尊重用户修改）。
func ensureIdentityDir(name string, config []byte, prompts map[string][]byte) {
    dir := filepath.Join(identitiesDir, name)
    os.MkdirAll(filepath.Join(dir, "prompts"), 0755)

    // identity.md — 仅当不存在时写入
    configPath := filepath.Join(dir, "identity.md")
    if _, err := os.Stat(configPath); os.IsNotExist(err) {
        os.WriteFile(configPath, config, 0644)
    }

    // prompts/ — 仅当不存在时写入
    for filename, content := range prompts {
        p := filepath.Join(dir, "prompts", filename)
        if _, err := os.Stat(p); os.IsNotExist(err) {
            os.WriteFile(p, content, 0644)
        }
    }
}

// verifyDefaultPrompts 在每次启动时检查 default/prompts/ 的完整性。
// 如果任何文件缺失或被修改，从规范版本恢复。
func verifyDefaultPrompts() {
    for _, filename := range builtin.DefaultPromptFiles {
        diskPath := filepath.Join(defaultIdentityDir, "prompts", filename)
        canonical := builtin.ReadDefaultPrompt(filename)

        onDisk, err := os.ReadFile(diskPath)
        if os.IsNotExist(err) {
            log.Info("restoring missing default prompt: %s", filename)
            os.WriteFile(diskPath, canonical, 0644)
            continue
        }
        if err != nil {
            log.Warn("cannot read default prompt, restoring: %s (%v)", filename, err)
            os.WriteFile(diskPath, canonical, 0644)
            continue
        }
        if !bytes.Equal(onDisk, canonical) {
            log.Warn("default prompt was modified, restoring: %s", filename)
            os.WriteFile(diskPath, canonical, 0644)
        }
    }
}
```

关键行为：
- **`default/` 和 `readonly/` 目录在首次 init 时自动创建**
  — 仅当不存在时写入
- **默认 prompt 每次启动时验证** — 被修改的文件会被规范版本覆盖
- **`readonly/` 的 prompt 文件一旦存在，永远不会被覆盖**
- **用户创建的身份目录不受影响**
- **无静默升级** — 如果 prompt 跨版本发生变化，它会被覆盖
  （二进制中的规范版本始终是真相来源）

## Prompt 内容的来源

### 内置 prompt：编译到二进制中，提取到磁盘

内置身份的 prompt 内容**嵌入在二进制中**（通过 `//go:embed`），
并在 init 时写出到 `~/.gen/identities/`。

```
源码（嵌入在二进制中）:                    用户目录（init 后）:
internal/identity/builtin/                 ~/.gen/identities/
├── default/                              ├── default/
│   ├── identity.md                       │   ├── identity.md    ← init 时提取
│   └── prompts/                          │   └── prompts/
│       ├── output.txt                    │       ├── output.txt ← init 时提取
│       ├── engineering.txt               │       ├── engineering.txt
│       ├── guidelines.txt                │       ├── guidelines.txt
│       └── environment.txt               │       └── environment.txt
└── readonly/                             ├── readonly/
    └── identity.md                       │   ├── identity.md    ← init 时提取
                                          │   └── prompts/
                                          │       └── （空或用户创建）
                                          └── code-reviewer/     ← 用户创建
                                              ├── identity.md
                                              └── prompts/
                                                  └── output.txt
```

### 解析流程

```
┌─────────────────────────────────────────────────────────────────┐
│ 对于 identity.Sections 中的每个节名（按顺序）：                   │
│                                                                 │
│   <identity>/prompts/<section>.txt 存在？                       │
│     ├── 是 → 使用它（覆盖）                                      │
│     └── 否 → 回退到 default/prompts/<section>.txt               │
│                 ├── 存在 → 使用它（回退）                        │
│                 └── 不存在 → 跳过此节（不注入）                   │
│                                                                 │
│ 然后，始终追加：policy（框架注入）                                │
└─────────────────────────────────────────────────────────────────┘
```

```
示例：code-reviewer，sections: [output, engineering, guidelines, environment]

  output:
    code-reviewer/prompts/output.txt     → 存在 → 使用它（自定义）
  engineering:
    code-reviewer/prompts/engineering.txt → 不存在
    default/prompts/engineering.txt       → 存在 → 使用它（回退）
  guidelines:
    code-reviewer/prompts/guidelines.txt  → 不存在
    default/prompts/guidelines.txt        → 存在 → 使用它（回退）
  environment:
    code-reviewer/prompts/environment.txt → 不存在
    default/prompts/environment.txt       → 存在 → 使用它（回退）
  + policy（框架注入）

示例：readonly，sections: [environment]

  environment:
    readonly/prompts/environment.txt      → 不存在
    default/prompts/environment.txt       → 存在 → 使用它（回退）
  + policy（框架注入）
  （output、engineering、guidelines 不在 sections: 中 → 不加载）
```

## 最终系统 prompt 组装

```
default 身份 → sections: [output, engineering, guidelines, environment]
  + 框架注入: policy

  你是 Gen Code，...                                       ← 前言（来自 identity.md 正文）
  <output>                                                 ← default/prompts/output.txt
  ...
  </output>
  <engineering>                                            ← default/prompts/engineering.txt
  ...
  </engineering>
  <guidelines name="tool-usage">...</guidelines>           ← default/prompts/guidelines.txt
  <guidelines name="system-reminders">...</guidelines>
  ...
  <environment>                                            ← default/prompts/environment.txt
  date: 2026-06-05  cwd: /project  platform: darwin/arm64
  </environment>
  <policy>                                                 ← 框架：始终注入
  ...
  </policy>

readonly 身份 → sections: [environment]
  + 框架注入: policy

  你是一个 AI 助手。...                                     ← 前言
  <environment>                                            ← 回退到 default/prompts/environment.txt
  date: 2026-06-05  cwd: /project  platform: darwin/arm64
  </environment>
  <policy>                                                 ← 框架：始终注入
  ...
  </policy>

code-reviewer 身份 → sections: [output, engineering, guidelines, environment]
  + 框架注入: policy

  你是一名代码审查专家。...                                 ← 前言
  <output>                                                 ← code-reviewer/prompts/output.txt（覆盖）
  审查要彻底。引用文件路径和行号。
  </output>
  <engineering>                                            ← 回退到 default/prompts/engineering.txt
  ...
  </engineering>
  <guidelines>                                             ← 回退到 default/prompts/guidelines.txt
  ...
  </guidelines>
  <environment>                                            ← 回退到 default/prompts/environment.txt
  ...
  </environment>
  <policy>                                                 ← 框架：始终注入
  ...
  </policy>
```

## 用户体验

### 创建自定义身份

```bash
# 0. 查看默认 prompt 作为参考
ls ~/.gen/identities/default/prompts/
# output.txt  engineering.txt  guidelines.txt  environment.txt
cat ~/.gen/identities/default/prompts/output.txt
# 显示默认 output prompt — 用作参考

# 1. 创建目录
mkdir -p ~/.gen/identities/concise

# 2. 编写 identity.md（sections: 是完整的清单）
cat > ~/.gen/identities/concise/identity.md << 'EOF'
---
name: concise
description: 简洁模式
sections:
  - output
  - engineering
  - guidelines
  - environment
---

你是一个简洁、直接的编程助手。直奔主题。
EOF

# 3.（可选）覆盖某个节的 prompt
#    该节必须也在上面的 sections: 中声明
mkdir -p ~/.gen/identities/concise/prompts
cat > ~/.gen/identities/concise/prompts/output.txt << 'EOF'
<output>
直接回答。不说闲话。不道歉。
</output>
EOF

# 4.（可选）捆绑技能
mkdir -p ~/.gen/identities/concise/skills/short-answers
cat > ~/.gen/identities/concise/skills/short-answers/SKILL.md << 'EOF'
# 简短回答
始终在三行以内回答。
EOF

# 5. 切换到该身份
gen> /identity concise
✓ 身份已切换: concise
```

### 反模式：有覆盖文件但没有声明节

```bash
# 错误：output.txt 存在但 output 不在 sections: 中
cat > ~/.gen/identities/bad-identity/identity.md << 'EOF'
---
name: bad-identity
description: 这不会按预期工作
sections:
  - engineering
  - environment
---

我是一个错误示例。
EOF

cat > ~/.gen/identities/bad-identity/prompts/output.txt << 'EOF'
<output>
这永远不会被加载。节 "output" 不在 sections: 中
</output>
EOF

# 启动时：
# WARN: identity bad-identity: prompts/output.txt ignored — section "output" not declared in sections:
```

### 修改默认 prompt（不允许 — 会被还原）

```bash
# 这将在下次启动时被还原：
vim ~/.gen/identities/default/prompts/output.txt

# 下次启动时：
# WARN: default prompt was modified, restoring: output.txt
#
# 要自定义 output，请创建自己的身份：
# mkdir -p ~/.gen/identities/my-style/prompts
# cp ~/.gen/identities/default/prompts/output.txt \
#    ~/.gen/identities/my-style/prompts/output.txt
# vim ~/.gen/identities/my-style/prompts/output.txt
```

### 创建项目级身份

```bash
# 存放在仓库中，可以提交
mkdir -p .gen/identities/project-role

cat > .gen/identities/project-role/identity.md << 'EOF'
---
name: project-role
description: 项目特定约定
sections:
  - engineering
  - guidelines
  - environment
---

你正在 Gen Code 项目上工作。请遵循 Go 语言约定。
EOF
```

### 删除自定义身份

```bash
rm -rf ~/.gen/identities/my-custom
# 完全移除；其他身份不受影响
```

## 身份数据结构

```go
type Identity struct {
    Name        string     // 目录名，也是身份名称
    Description string     // 一行描述
    Preamble    string     // 身份声明（identity.md 的正文）
    Sections    []string   // 要加载的角色层节的完整列表，按顺序
    Tools       ToolPolicy // 工具限制（与权限层合并）
    Dir         string     // 此身份目录的路径
    SkillsDir   string     // 捆绑 skills/ 的路径（没有则为空）
}
```

## 可扩展性

### 添加新的内置身份

1. 在 `internal/identity/builtin/` 下创建一个包含 `identity.md` 的目录
2. 可选添加 `prompts/` 文件
3. 在 `Initialize()` 中添加一行：`ensureIdentityDir("new-name", ...)`
4. 完成。下次 `gen init` 时，它会被写入磁盘

### 添加新的角色层节类型

1. 在内置默认 prompt 中添加一个 `.txt` 文件
2. 在节→槽映射表中添加一行
3. 在其 `sections:` 列表中声明新节的现有身份将自动加载它
   （通过回退到 `default/prompts/`）
4. 未声明该节的身份不受影响

### 社区分享

```bash
# 导出一个身份（目录 tarball — 包含技能，如果已捆绑）
tar -czf my-architect.tar.gz -C ~/.gen/identities architect/

# 导入
tar -xzf my-architect.tar.gz -C ~/.gen/identities/
gen> /identity architect
```

## 与现有系统的关系

| 现有概念 | 变更 |
|---|---|
| `~/.gen/identities/*.md` | 扁平 `.md` 文件迁移到 `<name>/identity.md` 目录 |
| `prompts/*.txt`（嵌入式） | 默认 prompt 在 init 时提取到磁盘上的 `default/prompts/` |
| `.gen/identities/`（项目级） | **保留** — 名称冲突时项目级优先 |
| `applyDefaults()` | 硬编码的作用域分支替换为遍历 `Identity.Sections` |
| `core.Scope` | 保留 Main/Subagent 概念，但角色层组合由身份驱动 |
| Policy | **框架强制执行** — 始终注入，永远不可移除，永不在磁盘上 |
| `/identity` | 扫描 `.gen/identities/` 和 `~/.gen/identities/` |
| `settings.json` | `"identity": "readonly"` 指向目录名 |
| 技能加载器 | 当前身份的 `skills/` 作为搜索根添加 |
| 工具权限 | 合并到现有权限层，而非并行系统 |

## 默认 prompt 内容

`default/prompts/` 下的文件与当前 `prompts/*.txt` 内容一致。
它们在 init 时从二进制中提取，并在每次启动时进行完整性验证：

| 文件 | 对应槽位 | 描述 |
|---|---|---|
| `output.txt` | SlotIdentity | 语气、更新、行为 |
| `engineering.txt` | SlotIdentity | 约束、代码约定、错误处理 |
| `guidelines.txt` | SlotGuidelines | 工具使用、系统提醒、任务工作流、何时询问 |
| `environment.txt` | SlotEnvironment | 环境模板（含 `{{.Date}}` 等变量） |

`policy.txt` **不在**磁盘上——它由框架直接注入，
用户无法查看、修改或删除。

## 已确定的问题

1. **项目级身份目录？** → **保留两者。** `.gen/identities/`
   （项目级）和 `~/.gen/identities/`（用户级）。名称冲突时项目级优先。
   `identity/registry.go` 目前已同时解析两者。

2. **用户可以修改默认值吗？** → **不可以。** `default/prompts/` 文件在
   每次启动时进行完整性验证。被修改的文件会被规范版本覆盖。需要自定义
   的用户应该创建自己的身份目录。

3. **如果用户删除了 `default/prompts/` 会怎样？** → **下次启动时恢复。**
   缺失的文件会从二进制中重新提取。删除整个 `default/` 目录不会破坏系统——
   它会在下次 init 时重建。

4. **升级时是否应更新默认 prompt？** → **是，自动更新。**
   默认 prompt 每次启动时与二进制中的规范版本进行验证。当新版本发布更新
   的 prompt 时，它们会在下次启动时覆盖磁盘副本。在自身身份目录中自定义
   了 prompt 的用户不受影响。

5. **身份可以移除安全策略吗？** → **不可以。** Policy 不提取到磁盘、
   不列在 `sections:` 中，始终由框架注入。没有任何身份可以移除或修改它。

6. **如何处理现有的扁平 `.md` 身份文件？** → **一次性迁移。**
   升级后首次启动时，`~/.gen/identities/<name>.md` 被移动到
   `~/.gen/identities/<name>/identity.md`。非破坏性操作。

7. **为什么把默认值提取到磁盘而不是保持纯嵌入式？**
   → 用户需要一个参考来了解可以自定义什么。创建自定义身份时，
   `default/prompts/` 下的文件可以准确展示有哪些节、使用什么格式、
   以及当前的默认值是什么。没有这个，用户只能猜测。

8. **`sections:` 与 `prompts/` —— 哪个控制要加载什么？**
   → **`sections:` 是唯一真相来源。** 它是角色层节的完整声明式列表，
   按顺序排列。`prompts/` 文件仅为已在 `sections:` 中声明的节提供覆盖内容。
   属于未声明节的文件将被忽略并产生警告。

## 参考资料

- [identity.go](../../internal/identity/identity.go) — 当前 Identity 结构体
- [catalog.go](../../internal/core/system/catalog.go) — 当前系统 prompt 组装
- [section.go](../../internal/core/section.go) — Section 和 Slot 类型
- [prompts/](../../internal/core/system/prompts/) — 当前嵌入的 prompt 文件（将迁移到 init 时提取的内置默认身份目录）
