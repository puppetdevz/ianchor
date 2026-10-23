# iAnchor

通过符号链接将配置文件同步到 iCloud Drive。各台 Mac 完成目录和链接配置后，即可共享 dotfiles 和应用配置。

[English](README.en.md) · [更新日志](CHANGELOG.md)

## 工作原理

iAnchor 将配置文件/目录移动到 iCloud Drive 中的指定目录，然后在原始位置创建符号链接。iCloud 负责同步文件，符号链接确保应用程序仍能找到配置。

## 安装

需要 macOS、已启用的 iCloud Drive 和 Homebrew。

```bash
brew tap puppetdevz/puppetdevz
brew install puppetdevz/puppetdevz/ianchor
```

此个人 tap 对应仓库 [puppetdevz/homebrew-puppetdevz](https://github.com/puppetdevz/homebrew-puppetdevz)。

Formula 仅声明 `node` 为直接依赖，入口绑定 Homebrew Node，不受 nvm 等 PATH 配置影响。`better-sqlite3` 必须有匹配该 Node ABI、macOS 和架构的预编译包；下载或加载失败会中止安装，不回退源码编译，也不要求额外声明 Python。Node 自身仍可能有传递依赖，安装需要联网。Homebrew Node 跨 ABI 升级后，旧原生模块可能失效，可重新安装以获取匹配产物；没有产物时请报告错误，不要尝试源码编译。

## 升级

已从新 tap 安装的用户：

```bash
brew update
brew upgrade puppetdevz/puppetdevz/ianchor
```

### 从旧 tap 迁移

若原先从 `vissong/ianchor` 安装，先切换安装来源，不能仅替换升级命令：

```bash
brew uninstall ianchor
brew untap vissong/ianchor
brew tap puppetdevz/puppetdevz
brew install puppetdevz/puppetdevz/ianchor
```

若使用的旧 tap 是 `puppetdevz/ianchor`，将上面的 untap 命令替换为 `brew untap puppetdevz/ianchor`；只移除实际使用的旧 tap。

迁移只更换 Homebrew 安装来源，命令仍为 `ianchor`，安装仍位于 `$(brew --prefix)/Cellar/ianchor/<版本>`。卸载程序不会删除 iCloud 同步目录、追踪数据库或 `~/.config/ianchor.json`，无需先执行 `recover` 或重新初始化。

## 发布维护

源码仓库的 `Formula/ianchor.rb` 是带占位符的权威模板，不能直接安装。稳定版 `vMAJOR.MINOR.PATCH` 标签必须与 `package.json` 版本一致；Actions 使用该标签提交中的模板和源码归档 SHA256，将完整 Formula 同步到 tap 的 `main`，不在 tap 单独维护安装逻辑。预发布标签会被拒绝。

首次发布前，维护者需准备已存在、`main` 至少有一个提交的 `puppetdevz/homebrew-puppetdevz`，并在源码仓库配置可直接写入该分支的 `HOMEBREW_TAP_TOKEN`。缺少 `Formula/` 或目标 Formula 会自动创建；工作流不会创建仓库或修改分支保护。发布仅由维护者主动触发 release 流程，不移动旧标签来套用新工作流。

工作流用 Ruby 语法树读取元数据，不执行 tap 代码；只接受单个 `Ianchor < Formula` 类中的静态、无条件、直接 DSL 声明。动态/间接调用、条件元数据及不支持的类级结构会在发布前拒绝；维护者应核实实际版本后整理为静态声明再重跑，不要通过移除版本校验绕过。Release 查询失败只输出脱敏分类及修复指引，不回显原始错误。

同一版本/内容重跑不重复创建 Release 或提交，旧版本不得覆盖更高版本。同版本重跑可将 tap 恢复为该标签模板的渲染结果；标签创建后的模板修复需随新版本发布，不移动旧标签。Release 已创建而 tap 推送失败时保留 Release；修复权限/网络或远端竞争后可重跑同一标签。CI 使用当次 Homebrew Node 和锁文件验证，不保证未来所有 Node ABI 都有 SQLite 预编译产物，也不代表所有 Mac 架构都已实机验证。Formula 测试只在临时目录读写 SQLite，不执行真实 iCloud 初始化。

## 使用

### 初始化

```bash
ianchor init
ianchor init --dir dotfiles  # 自定义目录名
```

不带 `--dir` 时会交互询问目录名，回车使用 `ianchor`。默认同步目录为 `~/Library/Mobile Documents/com~apple~CloudDocs/ianchor/`，数据库位于该目录的 `ianchor.db`；目录选择保存在本机 `~/.config/ianchor.json`。

初始化会发现已有云端配置并登记，但不会自动建立本机符号链接；其他设备也需初始化，并为已知原始路径的条目执行 `relink`。

### 添加配置文件

```bash
ianchor add ~/.zshrc
ianchor add ~/.ssh --name ssh-config
ianchor add ~/.config/alacritty
```

未指定 `--name` 时，名称开头的点会被移除，例如 `~/.zshrc` 的追踪名称为 `zshrc`。后续命令请使用 `ianchor list` 显示的名称；已有条目的名称不会自动更改。

### 查看已追踪的配置

```bash
ianchor list
ianchor list --json
```

### 恢复配置文件

仅在当前设备上将 iCloud 中的文件或目录拷贝回原始位置，替换符号链接为独立的本地副本。保留云端文件及数据库记录，其他设备的链接不受影响。恢复后本机的修改不再同步，`ianchor list` 会显示为 `unlinked`，可用 `ianchor relink <name>` 重新加入同步。

恢复前会备份云端内容到 `<同步目录>/.bak/<name>`，替换同名旧备份。`--force` 会删除原位置已有的本地文件或目录；此处备份的是云端内容，不是被覆盖的本地内容。若条目缺少原始路径，则报错且保留云端数据。

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

`package.json` 声明使用 MIT 许可证；当前仓库尚未附带 `LICENSE` 正文。
