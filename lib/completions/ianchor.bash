# Bash 3.2+ completion; source this file to register ianchor.
_ianchor_quote_replies() {
  local i candidate quoted suffix
  for ((i=0; i<${#COMPREPLY[@]}; i++)); do
    candidate="${COMPREPLY[i]}"
    suffix=' '
    if [[ "$1" == add && -d "$candidate" ]]; then
      candidate="${candidate%/}/"
      suffix=''
    fi
    printf -v quoted '%q' "$candidate"
    COMPREPLY[i]="$quoted$suffix"
  done
}

_ianchor_complete() {
  local cur command candidate options i positional=0 end_options=0
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  command="${COMP_WORDS[1]}"

  if [[ $COMP_CWORD -eq 1 ]]; then
    options='init list add recover relink remove help'
    [[ "$cur" == -* ]] && options='--help --version'
    while IFS= read -r candidate; do
      COMPREPLY[${#COMPREPLY[@]}]="$candidate"
    done < <(compgen -W "$options" -- "$cur")
    _ianchor_quote_replies ''
    return 0
  fi

  # Find whether a positional argument has already been supplied, skipping
  # option values. Never offer entry names for --name or --dir values.
  for ((i=2; i<COMP_CWORD; i++)); do
    if [[ $end_options -eq 1 ]]; then
      ((positional+=1))
      continue
    fi
    case "${COMP_WORDS[i]}" in
      --) end_options=1 ;;
      --name|--dir)
        [[ $i -eq $((COMP_CWORD-1)) ]] && return 0
        ((i+=1))
        ;;
      -*) ;;
      *) ((positional+=1)) ;;
    esac
  done

  if [[ "$cur" == -* && $end_options -eq 0 ]]; then
    options='--help'
    case "$command" in
      init) options+=' --dir' ;;
      list) options+=' --json' ;;
      add) options+=' --name --force' ;;
      recover) options+=' --force' ;;
      remove) options+=' --yes -y' ;;
    esac
    while IFS= read -r candidate; do
      COMPREPLY[${#COMPREPLY[@]}]="$candidate"
    done < <(compgen -W "$options" -- "$cur")
    _ianchor_quote_replies ''
    return 0
  fi

  [[ $positional -gt 0 ]] && return 0
  case "$command" in
    recover|relink|remove)
      while IFS= read -r candidate; do
        if [[ -n "$candidate" && "$candidate" == "$cur"* ]]; then
          # Readline replaces only the suffix after a colon word break.
          if [[ "$COMP_WORDBREAKS" == *:* && "$cur" == *:* ]]; then
            candidate="${candidate#"${cur%:*}:"}"
          fi
          COMPREPLY[${#COMPREPLY[@]}]="$candidate"
        fi
      done < <(ianchor __complete 2>/dev/null)
      ;;
    add)
      while IFS= read -r candidate; do
        COMPREPLY[${#COMPREPLY[@]}]="$candidate"
      done < <(compgen -f -- "$cur")
      ;;
    help)
      while IFS= read -r candidate; do
        COMPREPLY[${#COMPREPLY[@]}]="$candidate"
      done < <(compgen -W 'init list add recover relink remove help' -- "$cur")
      ;;
  esac
  _ianchor_quote_replies "$command"
  return 0
}
# Quote names ourselves: filenames mode incorrectly adds '/' to entry names
# that happen to match local directories. Append spaces only to complete words.
complete -o nospace -F _ianchor_complete ianchor
