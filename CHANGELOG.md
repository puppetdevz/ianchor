# 更新日志

[English](CHANGELOG.en.md)

## 1.0.0 - 2026-10-22

### 修复

- `recover` 仅恢复当前设备的独立本地副本，保留 iCloud 文件和共享数据库记录，避免其他设备的链接失效；缺少原始路径时拒绝恢复并保留云端数据。
- Homebrew 发布工作流改用 `puppetdevz/ianchor` 和 `puppetdevz/homebrew-ianchor`；已存在的 GitHub Release 不再重复创建，源码包下载失败时停止处理，下载成功后再计算 SHA256。

### 文档

- 默认 README 改为中文，保留英文版并补充升级说明；说明本地恢复与全局删除的区别。
- 修正 dotfile 追踪名称示例，补充安装前提、初始化与数据位置、恢复覆盖风险及重新链接不合并本地修改的说明。
- 新增中英双语更新日志，补齐可核实的历史版本记录。

### 行为说明

- `recover` 不再删除共享文件或追踪记录。恢复后本机修改不再同步，`list` 显示为 `unlinked`；使用 `relink <name>` 可重新加入同步（本地副本会备份为 `.bak`）。如需全局删除，请使用 `remove`，这会影响其他设备。

## 历史版本记录

以下记录依据 Git 中的版本变更和相关提交补录。日期为版本变更提交日期，不代表已核实的公开发布日期；本次检查时，本仓库本地及 `origin` 均无历史标签，GitHub 也无 Release 记录。

## 0.1.6 - 2026-04-04

### 维护

- 更新 `package.json` 和 `package-lock.json` 至 0.1.6；相对上一版本未发现其他代码变更。依据：[dbeb977](https://github.com/puppetdevz/ianchor/commit/dbeb97791d033294d1b4cef94ed6ed22c695effe)。

## 0.1.5 - 2026-04-04

### 重构

- 将 Homebrew formula 从主仓库移至独立 tap 仓库，发布工作流负责更新 tap；同步安装文档并移除旧发布脚本。依据：[888a1fe](https://github.com/puppetdevz/ianchor/commit/888a1fe0be281a12ba00d4e28eb8f256b1d2f7f0) 及此前提交。

## 0.1.4 - 2026-04-04

### 修复

- 版本递增时同时提交 `package-lock.json`，补齐 0.1.3 的锁文件版本。依据：[47abc79](https://github.com/puppetdevz/ianchor/commit/47abc79083eae00b48befbbfc25b38a0c9117025) 及此前提交。

## 0.1.3 - 2026-04-04

### 修复

- 将 zsh 与 bash 补全脚本分开生成；zsh 使用 `#compdef`、`_arguments` 和 `_describe`，修复自动加载格式。依据：[258621f](https://github.com/puppetdevz/ianchor/commit/258621fb500c4ac8a01e2aa9425577a232d86930) 及此前提交。

## 0.1.2 - 2026-04-04

### 新功能

- `list` 使用异步文件检查，并在等待超过 100ms 时显示加载动画。
- 为 zsh 和 bash 提供 shell 补全，支持补全已追踪的条目名。
- 新增标签触发的 GitHub Actions 发布工作流和版本标签脚本。

### 变更

- `--dir` 选项移至 `init` 子命令，并持久化目录选择；原有调用应改用 `ianchor init --dir <name>`。依据：[2eade3b](https://github.com/puppetdevz/ianchor/commit/2eade3b08ad797131c767f8c69a47378045bd4c9) 及此前提交。

## 0.1.1 - 2026-04-01

### 新功能

- 未指定自定义名称时，移除 dotfile 名称开头的点，例如 `.zshrc` 在 iCloud 中保存为 `zshrc`。依据：[af2257d](https://github.com/puppetdevz/ianchor/commit/af2257d7b835967eeb4a963333bded2980cec206)。

## 0.1.0 - 2026-04-01

### 新功能

- 初始实现：检测 iCloud Drive、SQLite 追踪数据库、初始化与已知配置发现、添加配置并建立符号链接、表格/JSON 列表、本地恢复、重新链接和移除命令。
- 提供 Homebrew formula；项目及命令从 `anchor` 更名为 `ianchor`。依据：[ccdca79](https://github.com/puppetdevz/ianchor/commit/ccdca79ddbc9a36cd651693535a9390d10476a1a) 及此前提交。

### 历史行为

- 此版本的 `recover` 会删除云端文件和数据库记录；1.0.0 已改为仅恢复本地副本。
