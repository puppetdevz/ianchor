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
    # Readline replaces only the suffix following its last colon word break.
    candidate="${candidate#"$2"}"
    printf -v quoted '%q' "$candidate"
    COMPREPLY[i]="$quoted$suffix"
  done
}

_ianchor_complete() {
  local cur command candidate options i first char decoded='' trim=''
  local positional=0 end_options=0
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  command="${COMP_WORDS[1]}"
  first=$COMP_CWORD

  # Reassemble colon-separated COMP_WORDS without changing COMP_WORDBREAKS.
  if [[ "$COMP_WORDBREAKS" == *:* ]]; then
    while [[ $first -gt 2 ]]; do
      if [[ "${COMP_WORDS[first-1]}" == : || "${COMP_WORDS[first]}" == : ]]; then
        ((first-=1))
        cur="${COMP_WORDS[first]}$cur"
      else
        break
      fi
    done
  fi
  # Undo literal backslash escaping from an earlier completion, never eval it.
  for ((i=0; i<${#cur}; i++)); do
    char="${cur:i:1}"
    if [[ "$char" == \\ && $((i+1)) -lt ${#cur} ]]; then
      ((i+=1))
      char="${cur:i:1}"
    elif [[ "$char" == : && "$COMP_WORDBREAKS" == *:* ]]; then
      # An escaped colon belongs to the word; only a real break is trimmed.
      trim="$decoded:"
    fi
    decoded+="$char"
  done
  cur=$decoded

  if [[ $COMP_CWORD -eq 1 ]]; then
    options='init list add recover relink remove help'
    [[ "$cur" == -* ]] && options='-h --help -V --version'
  else
    # Skip flags and option values when locating the single positional argument.
    for ((i=2; i<first; i++)); do
      if [[ $end_options -eq 1 ]]; then
        ((positional+=1))
        continue
      fi
      case "${COMP_WORDS[i]}" in
        --) end_options=1 ;;
        --name|--dir)
          [[ $i -eq $((first-1)) ]] && return 0
          ((i+=1))
          ;;
        --name=*|--dir=*|-*) ;;
        *) ((positional+=1)) ;;
      esac
    done
    if [[ "$cur" == -* && $end_options -eq 0 ]]; then
      options='-h --help'
      case "$command" in
        init) options+=' --dir' ;;
        list) options+=' --json' ;;
        add) options+=' --name --force' ;;
        recover) options+=' --force' ;;
        remove) options+=' --yes -y' ;;
      esac
    elif [[ $positional -eq 0 ]]; then
      case "$command" in
        recover|relink|remove)
          while IFS= read -r -d '' candidate; do
            # A hyphen name needs an explicit -- so Commander sees an argument.
            [[ "$candidate" == -* && $end_options -eq 0 ]] && continue
            [[ -n "$candidate" && "$candidate" == "$cur"* ]] && COMPREPLY[${#COMPREPLY[@]}]="$candidate"
          done < <(ianchor __complete 2>/dev/null)
          ;;
        add)
          while IFS= read -r candidate; do
            COMPREPLY[${#COMPREPLY[@]}]="$candidate"
          done < <(compgen -f -- "$cur")
          ;;
        help) options='init list add recover relink remove help' ;;
      esac
    fi
  fi
  if [[ -n "$options" ]]; then
    while IFS= read -r candidate; do
      COMPREPLY[${#COMPREPLY[@]}]="$candidate"
    done < <(compgen -W "$options" -- "$cur")
  fi
  _ianchor_quote_replies "$command" "$trim"
  return 0
}
# Quote names ourselves: filenames mode incorrectly adds '/' to entry names
# that happen to match local directories. Append spaces only to complete words.
complete -o nospace -F _ianchor_complete ianchor
