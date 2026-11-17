# iAnchor

通过符号链接将配置文件同步到 iCloud Drive。各台 Mac 完成目录和链接配置后，即可共享 dotfiles 和应用配置。

[English](README.en.md) · [更新日志](CHANGELOG.md)

## 工作原理

iAnchor 将配置文件/目录移动到 iCloud Drive 中的指定目录，然后在原始位置创建符号链接。iCloud 负责同步文件，符号链接确保应用程序仍能找到配置。

跨磁盘迁移会在删除原件前校验内容、目录结构、符号链接目标和普通权限；失败时保留原件。ACL、扩展属性、硬链接关系和其他隐藏元数据不保证保留；本地校验也不代表 iCloud 上传完成。涉及重要文件时，请另外备份，并避免迁移期间修改文件；程序无法阻止已有文件句柄在校验后继续写入。提交后清理原件失败时，迁移已生效，报错中的残留原件也可能已被部分删除。

## 安装

需要 macOS、已启用的 iCloud Drive 和 Homebrew。

```bash
brew tap puppetdevz/tap
brew install puppetdevz/tap/ianchor
```

此个人 tap 对应仓库 [puppetdevz/homebrew-tap](https://github.com/puppetdevz/homebrew-tap)。

Formula 从源码构建 Go 程序，`go` 仅作为构建依赖，运行时不需要 Node，也不受 nvm 等 PATH 配置影响。SQLite 由纯 Go 实现（`modernc.org/sqlite`）静态编译进二进制，安装后没有原生模块、Node ABI 或 Python 依赖。构建阶段需要联网下载 Go 模块并使用 Homebrew 的 `go`；支持 macOS 13 及以上的 Apple Silicon 与 Intel。

## 升级

已从新 tap 安装的用户：

```bash
brew update
brew upgrade puppetdevz/tap/ianchor
```

### 从旧 tap 迁移

若原先从 `vissong/ianchor` 安装，先切换安装来源，不能仅替换升级命令：

```bash
brew uninstall ianchor
brew untap vissong/ianchor
brew tap puppetdevz/tap
brew install puppetdevz/tap/ianchor
```

若使用的旧 tap 是 `puppetdevz/puppetdevz` 或 `puppetdevz/ianchor`，将上面的 untap 命令替换为 `brew untap puppetdevz/puppetdevz` 或 `brew untap puppetdevz/ianchor`；只移除实际使用的旧 tap。

迁移只更换 Homebrew 安装来源，命令仍为 `ianchor`，安装仍位于 `$(brew --prefix)/Cellar/ianchor/<版本>`。卸载程序不会删除 iCloud 同步目录、追踪数据库或 `~/.config/ianchor.json`，无需先执行 `recover` 或重新初始化。

### 从 Node 实现升级

Go 重写版以兼容 Node 版命令、输出、退出码、补全脚本和数据布局为目标；危险名称、悬空链接和特殊文件等安全场景存在经批准的行为差异；`recover` 遇到指向其他现存对象的软链接时默认拒绝，需 `--force` 才替换。同步目录、`ianchor.db` 结构、`.bak` 备份、`~/.config/ianchor.json` 均未改变，原地即可继续使用，不需要 `recover`、重新 `init` 或迁移数据。升级后首次运行时若同步目录里存在旧版本留下的 `ianchor.db-wal`/`-shm` 文件，Go 版会照常读取并自行管理它们。

## 发布维护

源码仓库的 `Formula/ianchor.rb` 是带占位符的权威模板，不能直接安装。稳定版 `vMAJOR.MINOR.PATCH` 标签必须与 `VERSION` 文件一致；Actions 使用该标签提交中的模板和源码归档 SHA256，将完整 Formula 同步到 tap 的 `main`，不在 tap 单独维护安装逻辑。预发布标签会被拒绝。标签不可移动：已存在的标签不会被删除或重指，修模板须发新版本。

首次发布前，维护者应配置并核实源码仓库对已发布标签的服务端保护（禁止删除或强制更新）；脚本的多次检查并非原子锁，不能单独保证标签不被外部并发移动。还需准备已存在、`main` 至少有一个提交的 `puppetdevz/homebrew-tap`，并在源码仓库配置可直接写入该分支的 `HOMEBREW_TAP_TOKEN`。若使用 fine-grained PAT，需将 `puppetdevz/homebrew-tap` 纳入授权仓库并授予 Contents 读写权限；旧 token 若只授权旧仓库，必须更新该 secret。缺少 `Formula/` 或目标 Formula 会自动创建；工作流不会创建仓库或修改分支保护。发布仅由维护者主动触发 release 流程，不移动旧标签来套用新工作流。

工作流用 Ruby 语法树读取元数据，不执行 tap 代码；只接受单个 `Ianchor < Formula` 类中的静态、无条件、直接 DSL 声明。动态/间接调用、条件元数据及不支持的类级结构会在发布前拒绝；维护者应核实实际版本后整理为静态声明再重跑，不要通过移除版本校验绕过。Release 查询失败只输出脱敏分类及修复指引，不回显原始错误。

同一版本/内容重跑不重复创建 Release 或提交，旧版本不得覆盖更高版本。同版本重跑可将 tap 恢复为该标签模板的渲染结果；标签创建后的模板修复需随新版本发布，不移动旧标签。Release 已创建而 tap 推送失败时保留 Release；修复权限/网络或远端竞争后可重跑同一标签。CI 用 `go vet`、`go test`、`go test -race` 和 darwin/amd64、darwin/arm64 交叉构建验证源码，但不代表两个架构都已在实机验证。本地测试要求所有含语句的包保持 100% 语句覆盖，门禁为 `./scripts/coverage-check.sh <coverprofile>`：它按函数逐项要求 100.0%，并按整数统计要求零条未覆盖语句，因此 `go tool cover -func` 四舍五入后的总计无法掩盖缺口。覆盖错误分支的注入点默认调用真实实现。Formula 在临时目录中完成 init/add/list/recover 往返，只写入临时目录，不触碰真实 iCloud 数据。

## 使用

### 初始化

```bash
ianchor init
ianchor init --dir dotfiles  # 自定义目录名
```

不带 `--dir` 时会交互询问目录名，回车使用 `ianchor`。默认同步目录为 `~/Library/Mobile Documents/com~apple~CloudDocs/ianchor/`，数据库位于该目录的 `ianchor.db`；目录选择保存在本机 `~/.config/ianchor.json`。

初始化会发现已有云端配置并登记，但不会自动建立本机符号链接；其他设备也需初始化，并为已知原始路径的条目执行 `relink`。

共享数据库保存的是添加时的绝对原始路径，不会按当前设备自动改写；跨设备重新链接要求原始路径一致（包括用户名和配置位置），路径不一致时不要直接执行 `relink`。

### 添加配置文件

```bash
ianchor add ~/.zshrc
ianchor add ~/.ssh --name ssh-config
ianchor add ~/.config/alacritty
```

未指定 `--name` 时，名称开头的点会被移除，例如 `~/.zshrc` 的追踪名称为 `zshrc`。后续命令请使用 `ianchor list` 显示的名称；已有条目的名称不会自动更改。

若同名记录仍在数据库中、但 iCloud 中的对应文件已丢失，`add` 会报错提示该名称仍被追踪，不会沿用旧记录的原始路径。此时用 `ianchor add --force` 接管该条目，或先执行 `ianchor remove <name>`。

macOS 上同一个文件可以有多种大小写拼写，因此与已追踪条目仅大小写不同的名称会被拒绝：不带 `--force` 且已追踪条目的云端副本缺失时，`add` 报错并指明该已追踪条目。`remove` 仅在另一条目仍在追踪同一个文件时拒绝并指明该条目；在 iCloud 目录内改动副本的大小写拼写不影响删除。

### 查看已追踪的配置

```bash
ianchor list
ianchor list --json
```

### 恢复配置文件

仅在当前设备上将 iCloud 中的文件或目录拷贝回原始位置，替换符号链接为独立的本地副本。保留云端文件及数据库记录，其他设备的链接不受影响。恢复后本机的修改不再同步，`ianchor list` 会显示为 `unlinked`，可用 `ianchor relink <name>` 重新加入同步。

恢复前会备份云端内容到 `<同步目录>/.bak/<name>`，替换同名旧备份。提交前会比较恢复副本和备份的内容、目录结构、普通权限及链接目标；若不一致则中止并尝试回滚。复制与比较期间，新副本可能暂时增加所有者访问权限，校验后恢复最终普通权限；进程崩溃可能留下权限较宽的临时副本。恢复不保证保留 ACL；若最终权限使当前用户无法读取副本或读取并搜索目录，会中止并尝试回滚；未使用 `--force` 的冲突分支也不会保留不可访问的新备份。此检查不是原子快照，无法阻止检查后发生的并发修改。`--force` 会删除原位置已有的本地文件或目录；此处备份的是云端内容，不是被覆盖的本地内容。若条目缺少原始路径，则报错且保留云端数据。

```bash
ianchor recover zshrc
ianchor recover --force zshrc  # 覆盖已存在的文件
```

### 重新链接

为未链接的文件重新创建符号链接；需要已有追踪记录、原始路径及云端内容。若原位置存在本地文件或目录，会先重命名为 `<原路径>.bak`，再链接到云端副本，不会合并本地修改。

```bash
ianchor relink zshrc
```

### 移除追踪

从数据库中删除记录并删除 iCloud 中的文件。**这是全局删除，会导致其他设备指向该文件的链接失效；仅需当前设备退出同步时，请使用 `recover`。**

若旧记录的原始路径指向 iCloud 同步目录内部或其他危险位置（例如旧版本留下的异常数据），`remove` 会整体拒绝且不改动云端数据与数据库；请先修正该记录的原始路径后再移除。

```bash
ianchor remove zshrc
ianchor remove -y zshrc  # 跳过确认
```

## Shell 补全

支持补全子命令、选项、`add` 的本地路径，以及 `recover/relink/remove` 的已追踪条目名。**输入 `ianchor`（末尾有空格）再按 Tab 才是补子命令；没有空格时补的是可执行文件名。**

含空格、冒号或 shell 特殊字符的名称会按字面值转义；建议输入普通名称前缀再按 Tab，不要直接输入未转义的 `$()` 或反引号（它们会先被 shell 解释）。以 `-` 开头的条目必须放在 `--` 后，例如 `ianchor recover -- -name`；含换行、回车或 NUL 的条目不提供补全。bash 默认可能需要按两次 Tab 才显示多个候选。

### Homebrew 安装

Formula 会安装 zsh 和 bash 补全文件。zsh 需要在运行 `compinit` **之前**包含 Homebrew 补全目录：

```zsh
fpath=("$(brew --prefix)/share/zsh/site-functions" $fpath)
autoload -Uz compinit
compinit
```

如果使用 Oh My Zsh 等会自动运行 `compinit` 的框架，只需将 `fpath` 那行放在框架初始化之前，不要重复初始化。bash 需要启用 Homebrew 的 bash-completion，或使用下面的手动加载方式。

### 手动加载

`ianchor completion zsh/bash` 只输出脚本，不会启用补全。当前 zsh 会话可在 `compinit` 之后运行：

```zsh
source <(ianchor completion zsh)
```

bash 会话使用：

```bash
source <(ianchor completion bash)
```

需要持久生效时，将对应的 `source` 行放入 shell 启动文件（zsh 的 `~/.zshrc`，bash 的 `~/.bashrc`；登录 bash 还需从 `~/.bash_profile` 加载 `~/.bashrc`）。也可以把 zsh 脚本保存为某个 `$fpath` 目录中的 `_ianchor`，随后重新运行 `compinit`，无需再手动 source。

升级后仍出现 `_ianchor: function definition file not found` 时，先检查 `$fpath` 中的 `_ianchor` 是否存在；补全文件就位后再删除旧 `~/.zcompdump*` 缓存并重开终端。

## License

本项目使用 MIT 许可证；当前仓库尚未附带 `LICENSE` 正文。
