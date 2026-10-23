#compdef ianchor

_ianchor() {
  local context state state_descr line
  local -A opt_args
  local -a commands entries expl
  commands=(
    'init:Initialize ianchor'
    'list:List all tracked config files'
    'add:Add a file or directory to iCloud'
    'recover:Restore a local copy while preserving iCloud data'
    'relink:Re-create a symlink for a tracked entry'
    'remove:Delete an entry and its iCloud copy'
    'help:Show command help'
  )

  _arguments -C -S \
    '(-h --help)'{-h,--help}'[Show help]' \
    '(-V --version)'{-V,--version}'[Show version]' \
    '1:command:->commands' \
    '*::argument:->arguments'

  case "$state" in
    commands)
      _describe -t commands 'ianchor command' commands
      ;;
    arguments)
      case "$words[1]" in
        init)
          _arguments '--dir[iCloud config directory name]:directory name:' '(-h --help)'{-h,--help}'[Show help]'
          ;;
        list)
          _arguments '--json[Output JSON]' '(-h --help)'{-h,--help}'[Show help]'
          ;;
        add)
          _arguments '--name[Custom entry name]:entry name:' '--force[Overwrite existing file]' \
            '(-h --help)'{-h,--help}'[Show help]' '1:source:_files'
          ;;
        recover)
          _arguments -S '--force[Overwrite existing local file]' \
            '(-h --help)'{-h,--help}'[Show help]' '1:entry:->entries'
          ;;
        relink)
          _arguments -S '(-h --help)'{-h,--help}'[Show help]' '1:entry:->entries'
          ;;
        remove)
          _arguments -S '(-y --yes)'{-y,--yes}'[Skip confirmation]' \
            '(-h --help)'{-h,--help}'[Show help]' '1:entry:->entries'
          ;;
        help)
          _describe -t commands 'ianchor command' commands
          ;;
      esac
      if [[ "$state" == entries ]]; then
        entries=("${(@0)$(ianchor __complete 2>/dev/null)}")
        # Leading hyphens are arguments only after an explicit option terminator.
        if (( ! ${words[(I)--]} )); then
          entries=("${(@)entries:#-*}")
        fi
        _wanted entries expl 'tracked entry' compadd -a entries
      fi
      ;;
  esac
}

# An autoloaded function must complete now; sourcing/eval must only register it.
if [[ "${funcstack[1]}" == _ianchor ]]; then
  _ianchor "$@"
else
  compdef _ianchor ianchor
fi
