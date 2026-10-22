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

可生成 zsh 或 bash 补全脚本，支持补全已追踪的条目名：

```bash
ianchor completion zsh
ianchor completion bash
```

## License

`package.json` 声明使用 MIT 许可证；当前仓库尚未附带 `LICENSE` 正文。
