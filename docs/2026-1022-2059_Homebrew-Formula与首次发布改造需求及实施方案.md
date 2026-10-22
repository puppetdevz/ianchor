# Homebrew Formula 与首次发布改造：需求及实施方案

- 编写时间：2026-10-22 20:59（本地 CST）。
- 状态：需求已确认，随后用户授权按本文实施；实现及自动验收记录见第 10 节。真实发布与 Homebrew 安装仍待用户手动验收。
- 原文档编写回合边界：当时只新增本 Markdown；后续实施授权覆盖该历史边界，但不包含真实发布、推送或用户 Homebrew 安装。
- 实施入口：本文件。真实发布必须由用户另行触发 `release` 技能，本文件不是发布授权。

### 后续实施授权补充

实施中实际复现了 `better-sqlite3 12.8.0` 缺少 Node 26 / ABI 147 / darwin-arm64 预编译产物（下载返回 404）。用户随后明确选择允许仅升级 SQLite 依赖及锁文件、寻找最小可用升级，仍禁止源码编译。首个支持 Node 26 且实际下载成功的版本为 `12.10.0`（`12.9.0` 无该产物）。

因此，本补充只覆盖原文中“不得修改依赖版本、package.json、package-lock.json”的相关限制：允许把 `better-sqlite3` 范围更新为 `^12.10.0`、锁定到 `12.10.0`，其他依赖、项目版本号及 changelog 不变。第 6 节中锁文件零差异的标准，是指安装/测试不再修改已授权更新后的锁文件；原始锁文件的唯一例外是本次 SQLite 升级。

## 1. 背景、目标与非目标

### 1.1 背景

iAnchor 是使用 Node.js、ES modules 和 `better-sqlite3` 的 macOS CLI。用户暂不采用独立可执行文件、内置 Node 或语言重写方案，转而完善 Homebrew Formula 和 GitHub Actions 发布链路。

当前工作流只能修改 tap 中已有的 Formula，缺失时会主动报错，无法完成首次初始化。本次后续改造需使同一套流程覆盖首次创建、后续更新和失败重跑。

### 1.2 已确认目标

1. 在源码仓库新增 `Formula/ianchor.rb`，作为发布模板的权威来源。
2. 优化 `.github/workflows/release.yml`：支持 tap 已存在但缺少 Formula 的首次发布。
3. Formula **仅声明 `depends_on "node"`**，不声明 `node@22`、Python 或额外的 `depends_on :macos`。
4. `better-sqlite3` 仅使用与实际 Node、macOS 和架构匹配的预编译模块；缺少产物必须失败，不回退源码编译。
5. 保持 CLI、数据库、配置与 iCloud 路径完全兼容。
6. 支持稳定版本发布、禁止降级、失败重跑和无变化时零提交。

> “只声明 node”是直接依赖声明约束，不代表 Node 自身没有 Homebrew 传递依赖，也不代表安装不需要网络。项目仍以 macOS 为支持平台；删除额外依赖声明不等于新增 Linux 支持。预编译 SQLite 产物缺失时，不保证安装成功，不得以偷偷安装 Python、源码编译或切换 Node 版本来掩盖失败。

### 1.3 非目标

- 不打包独立可执行文件，不内置 Node，不迁移 Bun、Go 或 Rust。
- 不修改 `bin/`、`lib/` 的业务实现、数据库结构、命令输出或交互行为。
- 原约束为不修改依赖版本、`package.json`、`package-lock.json`、项目版本号、双语 changelog 或 `scripts/tag.sh`；SQLite 依赖及对应锁文件项的后续唯一授权例外见开头补充。
- 不构建或发布 Homebrew bottle，不发布 npm 包，不新增 shell 补全功能。
- 不自动创建 GitHub 仓库、不初始化无提交的空 tap，不改分支保护或授予 token 权限。
- 不新增 `workflow_dispatch`，不自动回滚 Release，不删除、移动或强推标签。
- 不支持 alpha、beta、rc 等预发布版本；不保证未来所有 Node 主版本都有匹配原生模块。
- 不宣称 Intel 与 Apple Silicon 均已通过真实安装验收：用户只承诺在自己的一台 Mac 上验证。

## 2. 需求确认时的现状与涉及模块

以下为需求确认时的本地只读检查结果，作为改造基线保留；实施后的状态见第 10 节。不代表已经核实远端仓库、权限或线上 Actions 状态。

| 文件/位置 | 当前事实与影响 |
| --- | --- |
| `.github/workflows/release.yml:3-25` | `v*` 标签触发，`macos-latest`，固定 Node 22，执行 `npm install` 和 `npm test`。 |
| `.github/workflows/release.yml:27-39` | 先创建 GitHub Release，再更新 tap；已有 Release 会跳过创建。当前将所有查询失败视为“不存在”，需区分权限/网络错误。 |
| `.github/workflows/release.yml:44-64` | 从 `puppetdevz/ianchor` 标签归档下载源码；tap 为 `puppetdevz/homebrew-puppetdevz`。Formula 缺失立即失败，仅以 sed 替换已有 URL、SHA256。 |
| `.github/workflows/release.yml:67-73` | 只更新 `Formula/ianchor.rb`，有变化才提交并直接推送 `main`；目前仅检查普通 diff，不适用于首次新增未跟踪文件。 |
| `test/release-workflow.test.js:11-19` | 按步骤名称和 YAML 缩进提取脚本；工作流重构时必须同步测试提取逻辑，避免测试空跑。 |
| `test/release-workflow.test.js:183-188` | 明确保护“缺少 Formula 即失败”的旧行为，必须先编写新需求复现测试再修复。 |
| `package.json:3-22` | 版本 `1.0.0`，入口 `./bin/ianchor.js`，依赖 `better-sqlite3`、`chalk`、`cli-table3`、`commander`；声明 Node `>=18.0.0`。 |
| `package-lock.json:61-72`、`package-lock.json:150-156` | 锁定 `better-sqlite3 12.8.0` 和 `commander 14.0.3`，其 Node 支持范围并不等于项目声明的 `>=18`。本次不修改声明或锁定依赖。 |
| `bin/ianchor.js:1-22` | 使用 `/usr/bin/env node`，读取相邻 `lib/` 和上级 `package.json`，版本输出仅为包版本号。不能只安装入口文件。 |
| `lib/db.js:18-23` | `openDb(configDir)` 实际创建 SQLite 数据库、启用 WAL、初始化表；仅执行 `--version` 不足以验证原生扩展。 |
| `lib/icloud.js:5-14`、`lib/icloud.js:36-40` | 默认数据目录为 `~/Library/Mobile Documents/com~apple~CloudDocs/ianchor/`，本地配置为 `~/.config/ianchor.json`。 |
| `README.md:16-20`、`README.en.md`、`AGENTS.md:48` | 已使用个人 tap：`puppetdevz/puppetdevz`；后续需同步解释模板来源、首次发布前提与预编译限制。 |
| `scripts/tag.sh:23-47` | 会递增版本、提交并推送，也可能删除同名标签；本任务不调用、不修改该脚本。 |

补充事实：

- 当前源码仓库没有 `Formula/ianchor.rb`，后续应新建模板，不复用历史旧地址、空 SHA 或旧入口。
- 本地 `v1.0.0` 指向早于最新个人 tap 工作流的提交；重跑旧标签不会自动使用当前 HEAD 的新工作流。不得默认通过移动旧标签解决。
- 本地 `node_modules` 中 `better-sqlite3` 为 `12.11.1`，与锁文件的 `12.8.0` 不同。现有安装成功不能作为锁文件产物验证证据。
- 锁文件使用的 npm 下载地址涉及腾讯镜像，发布环境可达性尚未核实。
- 调查开始时已有两项用户工作区删除：`docs/superpowers/plans/2026-04-01-anchor-implementation.md`、`docs/superpowers/specs/2026-04-01-anchor-design.md`。本次不恢复、不处理，也不依赖它们作为实施依据。

### 2.1 后续允许变更的文件

- 新增 `Formula/ianchor.rb`。
- 修改 `.github/workflows/release.yml`。
- 修改 `test/release-workflow.test.js`，必要时增加发布/Formula 相关测试文件。
- 必要时同步 `README.md`、`README.en.md`、`AGENTS.md`。

安装与渲染辅助逻辑应放在上述 Formula、工作流及测试内；如必须增加独立工程脚本、修改业务代码或锁文件，先报告阻塞并重新取得授权。原文档编写回合只允许新增本需求文档；后续实施授权及 SQLite 唯一例外见开头补充。

## 3. 功能需求

### REQ-001：模板与发布产物分离

- 主仓库 `Formula/ianchor.rb` 是权威模板，允许使用明确、可校验的 URL、版本和 SHA256 占位符。
- 模板不是直接 `brew install` 的对象；仅渲染并验证后的 tap Formula 可安装。
- 工作流使用本次标签所指提交中的模板，不从其他分支取“最新模板”。
- 每个发布字段必须唯一且成功替换；最终产物不允许残留任何占位符。
- 后续更新重新以权威模板生成整个目标 Formula，不能只更新旧 Formula 的 URL 和 SHA；tap 中的手工定制可能被覆盖，维护入口应统一回源仓库。

### REQ-002：依赖和运行入口

- Formula 的直接 `depends_on` 声明恰好一项：`depends_on "node"`。
- 不添加 Python、固定版 Node、SQLite 或 macOS 的额外 `depends_on` 声明。
- 安装完整应用目录与生产依赖到 `libexec`，入口明确绑定 `Formula["node"].opt_bin/"node"`，不受用户 PATH 中 nvm、其他 Node 或 Python 影响。
- 包版本读取和相对模块导入保持有效；不得为适配安装修改业务入口。

### REQ-003：禁止原生模块源码编译回退

- 依据现有锁文件安装依赖，不重新解析或升级依赖版本。
- 安装时先禁止依赖生命周期脚本自动运行，再显式执行经过审查的预编译获取步骤；CI 和 Formula 遵守相同原则。
- 不能直接运行 `better-sqlite3` 的默认 `prebuild-install || node-gyp rebuild --release` 安装脚本。
- 预编译下载必须使用实际运行 Node 的 ABI、当前架构和平台；不得复用另一个 Node 的 `.node` 文件。
- 下载失败、没有匹配产物或加载失败时非零退出，提供 Node 版本、ABI、平台、架构和失败阶段；不得执行 `node-gyp`、`npm rebuild`、编译器或 Python 作为补救。
- Homebrew 注入的 build-from-source 环境、npm 配置与缓存不得导致隐式编译；只能对明确的预编译获取调用做必要环境适配。
- 获取后必须实际打开 SQLite 并完成读写验证；不能仅检查文件存在或版本命令成功。

### REQ-004：首次发布前提与创建行为

- 源仓库固定为 `puppetdevz/ianchor`；tap 固定为 `puppetdevz/homebrew-puppetdevz`；目标分支固定为 `main`。
- 用户事先准备已存在、至少有一个提交且有 `main` 分支的 tap，以及有对应读写权限的 `HOMEBREW_TAP_TOKEN`。
- 允许缺少 `Formula/` 目录或 `Formula/ianchor.rb`，工作流自动创建并提交。
- 不需要存在上一个标签、上一个 GitHub Release 或旧 Formula；不得通过“上一版本”推导首次发布必需的数据。
- tap 不存在、不可读、`main` 缺失或 token 缺失时明确失败，不自动创建仓库或更改权限。

### REQ-005：标签、源码归档与校验和

- 只接受 `vMAJOR.MINOR.PATCH` 稳定版，数字遵守 SemVer 的非负整数、无多余前导零规则。
- 标签去掉 `v` 后必须与该提交中 `package.json` 的版本完全一致。
- 源码 URL 为 `https://github.com/puppetdevz/ianchor/archive/refs/tags/<TAG>.tar.gz`。
- 先成功下载到临时文件，再对该文件计算 SHA256；结果必须为 64 位十六进制字符。
- 下载失败不得渲染、创建 Release 或写入 tap；临时文件退出时清理。
- 不依赖上个标签生成发布所需信息；本次不增加复杂发布日志生成机制。

### REQ-006：首发、更新和幂等

- tap 缺少 Formula：暂存并提交新文件，不能因普通 `git diff` 看不到未跟踪文件而跳过。
- tap 已存在旧版本：渲染新 Formula，提交实际差异。
- 相同版本且内容完全相同：成功退出，新增提交数和 push 次数均为 0。
- 相同版本但模板或安装定义有变：允许正常同步，不重复创建 Release。
- 只允许暂存和推送 `Formula/ianchor.rb`，不得使用 `git add .` 或把其他 Formula、README 等文件带入提交。

### REQ-007：降级与并发保护

- 从现有 Formula 读取版本，采用版本语义比较，不得字符串排序。
- 目标版本低于 tap 已有版本时拒绝；等于时允许按 REQ-006 幂等同步。
- 已有 Formula 损坏或无法可靠识别版本时明确失败，不当作“首次发布”覆盖。
- 对同一 tap 的本工作流写入串行化，不自动取消已经执行中的发布。
- 写入前重新确认比较基准没有过期；发生远端竞争或非 fast-forward 时失败，不 force push、不覆盖他人提交，用户可在查明原因后重跑。

### REQ-008：预检、Release 与失败恢复

- 尽可能在创建 Release 前完成标签/版本、模板、token 是否存在、tap 可读性及 `main`、降级、归档下载、渲染和必要检查。
- 只读预检不等于已证明实际写权限；写入权限、保护规则或网络仍可能在最后失败，必须如实报告。
- 仅在确认对应 Release 不存在时创建；已有 Release 保留并继续同步 tap。
- 查询 Release 的认证、权限、网络或服务错误必须失败，不能一概当成“不存在”。
- 任何关键步骤失败均使 workflow 失败，日志给出阶段和补救方向，不输出 token。
- Release 已创建但 tap push 失败时保留 Release；重跑同一标签只补齐缺失部分，不回滚、不删除标签、不重复创建 Release。

### REQ-009：实际 Homebrew Node 验证

- CI 不再固定 Node 22，仅使用该次 runner 上通过 Homebrew 解析到的 `node`；实际安装、测试和预编译获取必须使用同一个解释器。
- 记录 Homebrew node 版本、Node 版本、ABI、macOS 和架构，不能用 runner 预装的其他 Node 冒充。
- CI 环境若不能解析或准备 Homebrew node，应失败，不回退到 Node 22 或其他运行时。
- 使用项目现有 npm 锁文件体系，不新增 pnpm 锁文件、不安装或升级业务依赖。
- 本次只验证实施/发布当时的具体版本，不承诺未来滚动 Node 更新一定兼容；后续发现不兼容时阻塞并重新决策。

### REQ-010：业务与数据兼容

- 所有现有命令、参数、默认行为、交互和用户数据路径保持不变。
- Homebrew 安装、升级和测试不执行真实 iCloud 初始化，不迁移或删除现有配置和数据库。
- 卸载仅删除 Homebrew 管理的应用文件，不主动删除用户同步目录、配置或数据库。
- SQLite 和文件操作测试使用临时目录，不访问真实用户 iCloud。

### REQ-011：测试、说明与变更边界

- 先为旧行为及新增边界编写复现/回归测试，再修改实现。
- 更新旧的“缺少 Formula 必须失败”断言，不得简单删除该测试而不补首次创建成功测试。
- 同步双语 README 的模板说明、首发前提、只有 node 的直接依赖及预编译限制；AGENTS.md 如涉及 Formula 权威来源变化，应同步修正。
- 不借机修复许可证正文、引擎声明、锁文件镜像等旁支问题，发现影响验收时单独报告。

## 4. 技术方案

### 4.1 发布流水线建议顺序

1. checkout 触发标签的提交，验证稳定标签格式和包版本；检查源码模板存在。
2. 解析/准备 Homebrew `node`，显式将其放入受控 PATH，打印版本、ABI 和平台信息。
3. 使用锁文件、禁用生命周期脚本安装依赖，显式获取 SQLite 预编译模块；真实加载和读写后运行完整测试。
4. 预检 token、tap 和 `main`；使用独立临时目录显式 checkout `main`，读取现有版本并拒绝降级。
5. 下载标签归档并计算 SHA256；从本次标签模板渲染候选 Formula，检查字段唯一性、占位符清零、Ruby 语法。
6. 区分“Release 确认不存在”与“查询错误”；必要时创建 Release。
7. 再确认 tap 基准有效，仅写入 `Formula/ianchor.rb`；定向 `git add` 后通过 staged diff 判断是否有差异。
8. 有差异才提交并直接 push `main`；无差异记录 no-op。任何失败非零退出并清理临时目录。

预检可按依赖关系提前，但不能把允许重复运行的步骤做成破坏性动作。并发控制不能替代版本比较与远端冲突检查；不承诺跨 GitHub Release 与 Git push 的原子事务。

### 4.2 Formula 模板建议结构

- 类名 `Ianchor < Formula`；正确的描述、homepage、MIT 元数据。
- 使用三个明确占位符，例如 `__IANCHOR_VERSION__`、`__IANCHOR_URL__`、`__IANCHOR_SHA256__`；这些是设计命名，实施时应统一，并保证每个恰好出现一次。
- 唯一直接依赖为 `depends_on "node"`。
- `install`：使用 Homebrew node/npm、锁定安装且禁用脚本、显式获取原生预编译模块、验证加载、安装完整应用与生产依赖、创建绑定 Homebrew Node 的入口。
- `test do`：断言 `ianchor --version` 等于 Formula 版本，再用安装后的 `lib/db.js` 在 `testpath` 创建临时目录和数据库，插入一条记录、读回并关闭数据库。
- 不调用真实 `ianchor init`；不新增用户配置、不触碰真实 iCloud。

预编译获取步骤应调用锁定依赖中已有的工具，不临时从网络安装最新版工具。具体工具入口和 npm 参数须在实施时结合 Homebrew 环境验证；不允许因工具不兼容而恢复默认源码编译回退链。

### 4.3 工作流测试设计

- 保留现有 Node `node:test` 与 mock shell 命令策略，不为 YAML 解析或发布测试随意增加生产依赖。
- `gh`、下载、远端 git、预编译获取等使用可控 fixture，覆盖成功与异常返回值。
- 首次新增文件与 staged diff 必须有真实临时 Git 仓库或等价可靠 fixture 验证，不能沿用只比较已存在文件的 mock 而遗漏新文件。
- 故障注入记录 Python、node-gyp、编译器、Release 创建、git add/commit/push 的调用次数；严禁失败后调用禁用操作。
- 使用受控临时目录和干净测试环境；不能因为宿主机装了 Python 或已有缓存而误以为“预编译限定”成立。
- 工作流拆分/重命名后同步 `stepScript(name)` 的提取逻辑，验证测试实际执行目标脚本。

## 5. 实施步骤与关卡

1. **核对前提与工作区**：记录用户已有变更；核实允许变更文件、Homebrew node 的实际版本、锁文件预编译产物支持情况。不可用即报告阻塞，未经额外授权不得改依赖解决；已授权的 SQLite 例外见开头补充。
2. **先写复现测试**：覆盖首次缺 Formula、首次无目录、未跟踪文件暂存、预编译缺失不编译、无差异、降级和查询错误等；确认新测试在旧实现上失败。
3. **编写模板**：实现 REQ-001～REQ-003 和隔离 SQLite 的 `test do`；不改 CLI 或数据库。
4. **改造工作流**：完成 Node 来源、预编译限定、首发渲染、预检、稳定版校验、幂等、并发及降级保护。
5. **同步测试和必要文档**：保留其他 tap 文件不变的断言，更新双语说明与规则中失效内容。
6. **Harness 执行自动验收**：按第 6.2 节逐项记录结果；全部通过才可报告自动验收完成。
7. **移交用户手动验收**：给出第 6.3 节步骤与实际产物信息；本任务不代替用户触发 release，不移动已有 `v1.0.0`。
8. **汇总状态**：分别报告实现、自动验收、手动验收状态；手动项未收到用户确认时不得声称全部验收通过。

## 6. 验收方式与标准

### 6.1 执行责任：混合验收

**Harness 自动验收范围：Harness 必须逐项执行验收操作，按验收标准验证直至全部通过，不得在未通过时声明完成。** 适用于第 6.2 节本地/隔离验证，不包含真实远端发布、修改用户 Homebrew 或访问真实 iCloud 的授权。

**用户手动验收范围：用户将手动验证，Harness 无需执行自动化验证，完成实施并给出手动验证步骤即可。** 此说明仅适用于第 6.3 节真实 GitHub 发布、tap 推送和单台 Mac Homebrew 安装，不豁免第 6.2 节。

手动项结果未知时只能表述为“实现与自动验收完成，真实发布/安装待用户手动验收”。原文档编写回合未执行实施测试；后续实施记录见第 10 节。

### 6.2 Harness 自动验收矩阵

至少覆盖以下场景，每个场景应有可复查的断言和结果；相关失败用例预期非零，成功用例预期退出码 0。

| 编号 | 场景 | 可量化通过标准 |
| --- | --- | --- |
| AUTO-01 | 锁定安装与完整回归 | 使用实际 Homebrew node；完整测试失败数 0；生产依赖版本与锁文件一致；锁文件 diff 为 0。 |
| AUTO-02 | 直接依赖与模板 | `depends_on` 恰好 1 项且为 `node`；三个发布占位字段各 1 处；模板与渲染产物 Ruby 语法检查均成功。 |
| AUTO-03 | 正确渲染 | URL、版本、SHA 与输入完全一致；SHA 为 64 位十六进制；占位符残留数 0。 |
| AUTO-04 | 缺失/重复字段 | 分别注入缺失或重复占位符，均非零退出；Release 创建、git commit/push 调用数均为 0。 |
| AUTO-05 | 首次缺 Formula | tap 有 main、无目标文件：创建目标文件，commit 1 次、push 1 次，版本与 SHA 正确。 |
| AUTO-06 | 首次无 Formula 目录 | tap 有 main、无 Formula 目录：成功创建目录和文件，commit 1 次、push 1 次。 |
| AUTO-07 | 更新与无关文件保护 | 旧版本更新后仅目标 Formula 改变；至少 1 个其他 Formula 和 tap README 的字节内容不变；提交路径恰为目标文件。 |
| AUTO-08 | 重跑无差异 | 同版本同内容：新增 commit 0 次、push 0 次、Release 创建 0 次。 |
| AUTO-09 | 同版本模板修复 | 相同版本但模板内容改变：仅目标 Formula 同步，commit 1 次、push 1 次，已有 Release 不重建。 |
| AUTO-10 | 稳定标签与版本一致 | 非法标签、预发布、标签/包版本不一致分别失败；均不创建 Release、不写 tap；合法稳定标签成功。 |
| AUTO-11 | 首个标签/Release | 没有历史标签或 Release 的 fixture 可完成创建，不请求上一版本。 |
| AUTO-12 | 降级和损坏 Formula | 如已有 `1.10.0`、目标 `1.9.0` 必须失败；无法解析已有版本也失败；Release 创建及 push 均为 0。 |
| AUTO-13 | token、tap、main 异常 | token 缺失、clone 失败、目标分支缺失分别非零退出；Release 创建和 push 为 0；日志无真实 token。 |
| AUTO-14 | 下载与 SHA 异常 | 下载失败后 checksum/渲染/commit/push 均不执行；无效 SHA 被拒绝；临时目录和归档已清理。 |
| AUTO-15 | Release 状态区分 | 已存在：创建 0 次；明确不存在：创建 1 次；认证/网络/服务错误：非零退出、创建 0 次。 |
| AUTO-16 | 部分发布与重跑 | 模拟 Release 创建成功、push 失败：失败且不删除 Release；下一次已有 Release 时只补 tap，不重复创建。 |
| AUTO-17 | 竞争与 push 拒绝 | 远端基准改变或非 fast-forward 时非零退出；force push 和改动他人文件的次数均为 0；串行配置不取消正在运行的任务。 |
| AUTO-18 | 预编译严格限定 | 模拟缺产物、下载错误和 ABI 加载错误：均非零退出；Python、node-gyp、编译器及 npm rebuild 调用次数均为 0；覆盖 CI 与 Formula 的安装逻辑。 |
| AUTO-19 | SQLite 与解释器 | 成功获取匹配产物后，用 Homebrew node 临时建库，写入至少 1 条记录并正确读回；PATH 放入另一 Node fixture 时入口仍选 Homebrew node。 |
| AUTO-20 | 业务/数据安全 | 原有业务测试失败数 0；Formula 测试使用临时目录；不执行真实 init；不访问真实 iCloud/用户配置，不修改业务文件和版本数据。 |

真实 `brew install`/`brew test` 不属于 Harness 必须自动执行项；AUTO-19 的本地原生模块加载、DB 读写与入口逻辑验证不能替代用户真实 Homebrew 验收。环境不具备 Homebrew node 或匹配预编译产物时应记录阻塞，不得把该项标记通过。

### 6.3 用户手动验收步骤

前提：由用户另行触发 `release` 技能，按该技能确认待发布版本。选择一个包含本次工作流和模板的新稳定标签；不得默认移动旧 `v1.0.0`。以下命令是未来验收说明，本次不执行。

1. **发布前准备**
   - 确认 tap 已存在、`main` 至少有一个提交，配置 `HOMEBREW_TAP_TOKEN` 并允许直接推送 `main`。
   - 记录 `sw_vers`、`uname -m`、`brew --version`、`brew list --versions node` 及实际 Node 版本/ABI。
   - 首次状态要求 tap 原本没有目标 Formula；不要为模拟首发删除线上已有 Formula。若远端已有文件且不能安全构造首发状态，真实首发验收标记待验证，自动 fixture 结果不能冒充真实首发结果。

2. **触发真实发布**
   - 通过单独授权的 release 流程发布包含新实现的标签，等待 Actions 完成。
   - 确认 Actions 使用 Homebrew node，预编译模块成功获取，全部测试通过，无源码编译回退。
   - GitHub 中对应标签 Release 恰好 1 个；tap 的 `main` 新增/更新目标 Formula，提交只包含该路径。
   - 核对 Formula 版本、归档 URL 和实际归档 SHA256，无占位符残留。

3. **单台 Mac 安装或升级**

   ```bash
   brew tap puppetdevz/puppetdevz
   brew update
   # 未安装时执行：
   brew install puppetdevz/puppetdevz/ianchor
   # 已安装旧版时改为：
   # brew upgrade puppetdevz/puppetdevz/ianchor
   ianchor --version
   ianchor --help
   brew test puppetdevz/puppetdevz/ianchor
   ```

   - 所执行命令退出码均为 0；版本输出与本次稳定标签去掉 `v` 后完全一致。
   - `--help` 保持现有命令集合；`brew test` 完成至少一次隔离 SQLite 写入和读回。
   - 检查安装入口绑定 Homebrew node，Formula 没有额外直接依赖；不能把系统原本存在 Python 当成安装必需性证明。
   - 不要求卸载系统 Python；“无源码编译回退”以安装步骤审查、日志与故障注入证据共同判断。

4. **幂等重跑**
   - 在同一标签已有的 Actions 运行上执行 GitHub 的重新运行。
   - Release 数仍为 1，tap 无新提交，工作流成功。
   - 若已有更高版本，旧版本重跑应拒绝降级而不是追求成功；使用当前版本完成幂等验证。

5. **兼容与记录**
   - 安装/升级前后确认已有 `~/.config/ianchor.json` 与 iCloud 中用户文件、数据库未被安装或测试改写；不对运行中的真实数据库强行做破坏性测试。
   - 用户记录实际验收的 macOS、架构、Node/ABI、标签、Actions 链接、tap 提交和各命令结果。
   - 如只有 Apple Silicon 或只有 Intel 的结果，另一架构明确写“未实机验证”，不阻止约定的单台 Mac 验收，但不能宣称双架构已通过。

## 7. 风险、阻塞与恢复策略

| 风险 | 处理方式 |
| --- | --- |
| 滚动版 node 超出锁定 `better-sqlite3` 的预编译支持范围 | 构建/安装明确失败；记录具体版本和 ABI，报告用户重新决策。不改依赖、不换 Node、不编译。 |
| Homebrew/npm 环境隐式要求源码构建 | 禁用自动脚本，显式走预编译路径；用故障测试证明无回退；无法实现时阻塞。 |
| 预编译下载站、npm 镜像或 GitHub 网络不可用 | 分阶段报错，保留必要诊断；适度网络重试不等于编译回退。 |
| token、分支保护、组织策略阻止 push | 用户调整权限后重跑；不自动放宽保护、不强推。 |
| Release 与 tap 跨仓库非原子 | 尽早预检；部分发布明确失败，保留 Release 并支持幂等补齐。 |
| 权威模板覆盖 tap 的人工修改 | README 明确权威来源；人工变更需回源仓库，不在 tap 长期维护分叉安装逻辑。 |
| 旧标签使用旧工作流 | 新实现应用于包含它的新版本；不移动旧标签。选定版本由用户触发的 release 流程决定。 |
| 修改 workflow 后测试提取失效 | 同步提取逻辑并验证每个目标步骤确实执行，不能靠测试数量下降伪装成功。 |
| 仅单台 Mac 实机验证 | 记录架构边界，不作双架构兼容承诺。 |

## 8. 待确认事项与实施前核验

### 8.1 需求决策状态

本轮需求分支已确认，没有需要继续猜测的产品/流程决策。不得将下面未核实的环境事实写成“已经通过”。

### 8.2 需求确认时列出的前提检查（历史记录）

以下清单保留原需求基线；第 2、3 项的本地安装与预编译部分已完成验证，SQLite 版本例外及结果见第 10 节。真实远端、用户手动验收环境及发布版本仍待后续确认。

1. 远端 `puppetdevz/homebrew-puppetdevz` 是否存在、是否已初始化 `main`、有无已有 Formula、token 和保护规则是否允许直接推送。
2. 实施/发布时 Homebrew node 的具体版本、ABI，以及锁定 `better-sqlite3 12.8.0` 是否有当前 Mac 架构匹配的预编译模块。
3. 禁用脚本后的锁定安装、显式预编译获取工具入口及 Homebrew 环境适配是否可行；网络与 npm 镜像是否可达。
4. 用户手动验收所用 Mac 的具体架构和 macOS；本轮未要求预先指定，由执行时记录。
5. 未来用于真实验收的新版本号与标签，由另行触发的 release 流程决定，不在本次文档编写中确定。

任何前提不满足时，应说明证据、影响和需要用户采取的动作；不能擅自扩大文件修改范围或降低验收标准。

## 9. 原文档编写回合交付核对

- 已实际加载并按 grilling 技能分轮提问，使用 harness 卡片工具收集回答，并取得共同理解确认。
- 已将用户最终选择覆盖前期候选方案：仅 `node`、预编译限定、只验证 Homebrew Node、混合验收。
- 仅新增本文件；不实施 Formula 或 workflow，不恢复用户已删除文档，不创建其他仓库文件。
- 后续应依据第 5、6 节实施和验收，不能将本文存在视为代码已经完成。

## 10. 实施与自动验收记录（2026-10-22）

### 10.1 已实现范围

- 新增 `Formula/ianchor.rb`：唯一直接依赖 node、完整应用目录、绑定 Homebrew Node 的入口、禁生命周期脚本的锁定安装、仅获取 SQLite 预编译模块、隔离 SQLite 实际读写测试。
- 更新 `.github/workflows/release.yml`：Homebrew Node、共享 Formula 中的预编译逻辑、串行发布、标签/版本校验、归档校验和、完整模板渲染、首发创建、定向暂存、无差异 no-op、数字段降级保护、Release 查询错误区分、失败重跑与远端竞争检查。
- 更新 `test/release-workflow.test.js`，新增 `test/formula.test.js`；同步 `README.md`、`README.en.md`、`AGENTS.md`。
- 按授权仅更新 SQLite 的依赖范围、锁定版本、下载地址、完整性值和对应引擎信息；其他锁定包保持不变。
- `bin/`、`lib/`、`scripts/tag.sh`、项目版本 `1.0.0` 及双语 changelog 未改动。本地提交依据后续收尾授权执行；未推送、移动标签、创建真实 Release 或执行 `brew install`/`brew test`。

### 10.2 验证环境和方法

- 实测解释器：Homebrew `/opt/homebrew/Cellar/node/26.12.0/bin/node`，Node `v26.12.0`，ABI `147`，`darwin/arm64`，macOS `27.0.1`（`26A434`）。
- 保留工作区原有 `node_modules`；另建临时应用副本，实际执行工作流的锁定依赖安装步骤，使用更新后的锁文件获取 `better-sqlite3 12.10.0`。
- SQLite 预编译 tarball 实际返回 HTTP 200，实际加载原生模块、内存建表/写入/读回，以及 `lib/db.js` 的临时 WAL 数据库探针均成功。
- 再将预编译下载代理指向不可连接的本地端口：实际下载阶段非零退出，给出 Node/ABI/架构诊断；Python、node-gyp 和编译器拦截日志调用数为 0。
- 在该锁文件隔离环境执行项目 `npm test`：**132 项通过，失败/取消/跳过均为 0**（含第 10.5 节提交后复审新增的 12 项）。另用 Ruby/Psych 检查 YAML，用 `bash -n` 检查全部 4 个工作流 shell 步骤，Ruby 语法、测试 JS 语法、Git whitespace 检查均通过。
- 发布测试使用真实临时 Git 仓库/index/bare remote；GitHub API、源码归档下载及凭据为 fixture，不触达真实 tap。正常 fixture 的 SHA256 由真实 `shasum -a 256` 对下载文件计算。
- Formula DSL 安装/测试 harness 模拟 Homebrew 路径 API、npm 安装与下载，但执行真实应用、真实 SQLite 和入口脚本；包含带空格的安装目录、PATH 中另一 Node、`1.0.0`/`2.3.4` 两种包版本。它不是实际 `brew install` 的替代证据。

### 10.3 自动验收逐项对应

| 编号 | 已检查的证据 |
| --- | --- |
| AUTO-01 | 实际 Homebrew Node 下锁定安装与 132 项完整回归；仅授权 SQLite 包变化，其他锁文件项一致。 |
| AUTO-02 | 三个唯一占位符、唯一 node 直接依赖；模板和渲染产物均通过 Ruby 语法检查。 |
| AUTO-03 | 下载 fixture 文件的真实 SHA256、URL、版本与渲染结果完全一致，无占位符残留。 |
| AUTO-04 | 缺字段、重复字段、未知/未闭合占位符、重复版本、额外依赖及注释/条件/方法体中的假声明均在创建 Release 前失败。 |
| AUTO-05 | 缺目标文件时真实暂存、提交、推送各一次，提交路径只有目标 Formula。 |
| AUTO-06 | 缺整个 Formula 目录时同样成功首发。 |
| AUTO-07 | 旧模板整体替换；其他 Formula、README、根目录文件按字节比较不变。 |
| AUTO-08 | 相同版本/内容重跑不提交、不 push、不创建 Release。 |
| AUTO-09 | 同版本模板修复提交一次，不重建已有 Release。 |
| AUTO-10 | 非稳定标签、前导零、版本不一致及 shell 注入标签被拒绝，无副作用。 |
| AUTO-11 | 单提交、无历史标签/Release 的 fixture 完成首次发布，不查询上一版本。 |
| AUTO-12 | BigInt 数字段比较拒绝降级；损坏/冲突、尾随注释版本覆盖、显式 receiver、同一行附加及反射调用均保守拒绝。元数据只接受单个 Formula 类体的无条件、直接、静态字面量 DSL；标准尾随注释可接受，注释和 heredoc 内的假版本不参与判断，不执行 tap Ruby。 |
| AUTO-13 | 缺 token、clone 失败、缺 main、路径为符号链接/错误类型均失败，日志中无 fixture token。只读检查不能证明远端写权限。 |
| AUTO-14 | 下载失败不计算 SHA、不发布；空值/非法 SHA 被拒绝；全部临时发布目录清理。 |
| AUTO-15 | 明确 Release 404 才创建；仓库查询失败、401/403/429/500、超时、坏 JSON/错误 tag 均不误创建。DNS/TLS/连接错误给出固定脱敏诊断和重跑指引，不回显含凭据的原始 stderr。 |
| AUTO-16 | Release 成功后模拟 push 失败，以及真实 bare remote hook 拒绝 push；保留 Release，重跑只补 tap。 |
| AUTO-17 | 固定并发组、不取消运行中任务；真实竞争提交导致失败，候选无差异也检查基准，不 force push。 |
| AUTO-18 | CI 直接复用 Formula 预编译脚本；下载/加载故障非零且编译命令调用为 0；额外执行真实下载连接失败检查。 |
| AUTO-19 | 匹配 ABI 的真实 SQLite 至少写入/读回 1 条；Formula harness 的安装和测试探针都成功；PATH 中其他 Node 不能接管入口。 |
| AUTO-20 | 原业务测试全部通过；业务文件与用户数据路径不改；全部测试仅使用临时数据，不运行真实 init。 |

### 10.4 审查与剩余手动项

独立只读审查发现两项问题：测试固定 `1.0.0` 会阻止下一版发布，以及带尾随注释的旧版本声明可能被忽略。两项均已先添加失败复现测试再修复，并纳入上述完整回归。

真实 GitHub/tap 权限、实际稳定标签发布、单台 Mac 的真实 Homebrew 安装/升级、`brew test` 与 Actions 重跑仍属于第 6.3 节用户手动验收，**尚未执行，不宣称已通过**。双架构兼容性没有实机验收结论。实现与自动验收已交付，手动步骤及权限前提已在第 6.3 节给出，供用户后续执行。

### 10.5 提交后对抗性复审与修复

针对实现提交 `f2c90b2` 的复审确认三类问题：行正则会漏掉 `self.version`、同一行附加声明及 `method(:version).call` 等间接调用，导致降级保护失效；Ruby 注释中的假声明或条件/方法体中的不生效声明仍能通过候选验证；Release 查询失败时丢弃 stderr，缺少网络故障诊断与修复方向。

修复遵循“语法存在不等于语义生效”：旧 Formula 和渲染产物共用 Ripper 解析器，只在一个 `Ianchor < Formula` 类体的白名单静态 DSL 子集内采信直接、无条件元数据，按 AST 的真实位置验证唯一字面量值。不执行 tap Ruby，也不尝试解释任意动态 Ruby；超出支持范围时明确失败，由维护者核实版本并整理静态声明，不能猜测低版本继续发布。Release 查询错误则只做固定分类和修复提示，原始 stderr 和凭据不输出。

新增 12 项回归覆盖 receiver/同一行/反射调用、注释与 heredoc、注释中的假元数据/依赖、条件/方法体依赖以及 DNS/TLS/网络错误。缺陷用例在原提交中可复现错误发布；修复后完整 132 项通过。完整回归在临时锁文件安装副本中执行工作流原始安装与测试步骤，Homebrew Node `v26.12.0`/ABI `147`/`darwin-arm64` 下真实下载 `better-sqlite3 12.10.0` 预编译包并加载/读写成功。工作区原有依赖、项目版本及 changelog 均未更改；未执行真实远端写入或用户手动验收。
