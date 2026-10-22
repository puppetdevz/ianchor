# iAnchor

通过符号链接将配置文件同步到 iCloud Drive，让所有 Mac 自动共享 dotfiles 和应用配置。

[English](README.en.md)

## 工作原理

iAnchor 将配置文件/目录移动到 iCloud Drive 中的指定目录，然后在原始位置创建符号链接。iCloud 负责同步文件，符号链接确保应用程序仍能找到配置。

## 安装

```bash
brew tap vissong/ianchor
brew install ianchor
```

## 升级

```bash
brew update
brew upgrade ianchor
```

## 使用

### 初始化

```bash
ianchor init
ianchor init --dir dotfiles  # 自定义目录名
```

### 添加配置文件

```bash
ianchor add ~/.zshrc
ianchor add ~/.ssh --name ssh-config
ianchor add ~/.config/alacritty
```

### 查看已追踪的配置

```bash
ianchor list
ianchor list --json
```

### 恢复配置文件

仅在当前设备上将 iCloud 中的文件或目录拷贝回原始位置，替换符号链接为独立的本地副本。保留云端文件及数据库记录，其他设备的链接不受影响。恢复后本机的修改不再同步，`ianchor list` 会显示为 `unlinked`，可用 `ianchor relink <name>` 重新加入同步。

恢复前仍会备份云端内容到 `.bak/`；若条目缺少原始路径，则报错且保留云端数据。

```bash
ianchor recover .zshrc
ianchor recover --force .zshrc  # 覆盖已存在的文件
```

### 重新链接

为未链接的文件重新创建符号链接。

```bash
ianchor relink .zshrc
```

### 移除追踪

从数据库中删除记录并删除 iCloud 中的文件。**这是全局删除，会导致其他设备指向该文件的链接失效；仅需当前设备退出同步时，请使用 `recover`。**

```bash
ianchor remove .zshrc
ianchor remove -y .zshrc  # 跳过确认
```

## License

MIT
