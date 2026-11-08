package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

func init() {
	register(&command{
		name: "completion", group: "Other", args: "<shell>", summary: "Print a shell completion script (bash, zsh, fish)",
		long: "Prints a completion script. Packages install these for you; to load one by hand, for example in bash: source <(beebox completion bash)",
		run: func(ctx context.Context, a *App, args []string) error {
			if len(args) != 1 {
				return usagef("name a shell: bash, zsh or fish")
			}
			switch args[0] {
			case "bash":
				fmt.Fprint(a.Stdout, bashCompletion())
			case "zsh":
				fmt.Fprint(a.Stdout, zshCompletion())
			case "fish":
				fmt.Fprint(a.Stdout, fishCompletion())
			default:
				return usagef("unsupported shell %q (use bash, zsh or fish)", args[0])
			}
			return nil
		},
	})
}

// flagNames lists each visible command's flags for the completion scripts.
var completionFlags = map[string][]string{
	"last":       {"--json", "--verbose", "--html"},
	"show":       {"--json", "--verbose", "--html"},
	"list":       {"--json", "--ids"},
	"view":       {"--print-path", "--sample", "--scenario"},
	"analyze":    {"--boot", "--ring", "--save", "--json", "--verbose", "--html"},
	"status":     {"--json"},
	"now":        {"--watch", "--json"},
	"doctor":     {"--json", "--strict"},
	"setup":      {"--yes", "--dry-run", "--skip"},
	"record":     {"--config"},
	"notify":     {"--wait", "--test"},
	"version":    {"--verbose", "--json"},
	"completion": {},
	"help":       {},
}

func visibleNames() []string {
	var out []string
	for _, c := range commands {
		if !c.hidden {
			out = append(out, c.name)
		}
	}
	sort.Strings(out)
	return out
}

func bashCompletion() string {
	var cases strings.Builder
	for _, n := range visibleNames() {
		fmt.Fprintf(&cases, "    %s) opts=\"%s --plain --no-color --no-banner --help\" ;;\n", n, strings.Join(completionFlags[n], " "))
	}
	return `# bash completion for beebox
_beebox() {
  local cur prev cmd opts
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  if [[ ${COMP_CWORD} -eq 1 ]]; then
    mapfile -t COMPREPLY < <(compgen -W "` + strings.Join(visibleNames(), " ") + ` --help --version --plain --no-color --no-banner" -- "$cur")
    return
  fi
  cmd="${COMP_WORDS[1]}"
  case "$prev" in
    --html|--ring|--config) mapfile -t COMPREPLY < <(compgen -f -- "$cur"); return ;;
    --boot) mapfile -t COMPREPLY < <(compgen -W "-1 -2 -3" -- "$cur"); return ;;
  esac
  case "$cmd" in
` + cases.String() + `    *) opts="" ;;
  esac
  if [[ "$cur" == -* ]]; then
    mapfile -t COMPREPLY < <(compgen -W "$opts" -- "$cur")
  elif [[ "$cmd" == show || "$cmd" == view ]]; then
    mapfile -t COMPREPLY < <(compgen -W "$(beebox list --ids 2>/dev/null)" -- "$cur")
  elif [[ "$cmd" == completion ]]; then
    mapfile -t COMPREPLY < <(compgen -W "bash zsh fish" -- "$cur")
  elif [[ "$cmd" == help ]]; then
    mapfile -t COMPREPLY < <(compgen -W "` + strings.Join(visibleNames(), " ") + `" -- "$cur")
  fi
}
complete -F _beebox beebox
`
}

func zshCompletion() string {
	var subs strings.Builder
	for _, c := range commands {
		if c.hidden {
			continue
		}
		fmt.Fprintf(&subs, "    '%s:%s'\n", c.name, strings.ReplaceAll(c.summary, "'", "'\\''"))
	}
	var cases strings.Builder
	for _, n := range visibleNames() {
		var specs []string
		for _, f := range completionFlags[n] {
			switch f {
			case "--html", "--ring", "--config":
				specs = append(specs, fmt.Sprintf("'%s[%s]:file:_files'", f, strings.TrimPrefix(f, "--")))
			case "--boot":
				specs = append(specs, "'--boot[boot offset or ID]:boot:(-1 -2 -3)'")
			case "--wait":
				specs = append(specs, "'--wait[how long to wait]:duration:'")
			default:
				specs = append(specs, fmt.Sprintf("'%s'", f))
			}
		}
		extra := ""
		switch n {
		case "show", "view":
			extra = " '1:report:_beebox_reports'"
		case "completion":
			extra = " '1:shell:(bash zsh fish)'"
		}
		fmt.Fprintf(&cases, "    %s) _arguments %s '--plain' '--no-color' '--no-banner' '--help'%s ;;\n", n, strings.Join(specs, " "), extra)
	}
	return `#compdef beebox
# zsh completion for beebox
_beebox_reports() {
  local -a ids
  ids=(${(f)"$(beebox list --ids 2>/dev/null)"})
  _describe 'report' ids
}
_beebox() {
  local -a subcmds
  subcmds=(
` + subs.String() + `  )
  if (( CURRENT == 2 )); then
    _describe 'command' subcmds
    return
  fi
  local cmd=${words[2]}
  shift words; (( CURRENT-- ))
  case $cmd in
` + cases.String() + `  esac
}
_beebox "$@"
`
}

func fishCompletion() string {
	var b strings.Builder
	b.WriteString("# fish completion for beebox\ncomplete -c beebox -f\n")
	for _, c := range commands {
		if c.hidden {
			continue
		}
		fmt.Fprintf(&b, "complete -c beebox -n __fish_use_subcommand -a %s -d %q\n", c.name, c.summary)
		for _, f := range completionFlags[c.name] {
			name := strings.TrimPrefix(f, "--")
			req := ""
			if name == "html" || name == "ring" || name == "config" {
				req = " -r -F"
			}
			fmt.Fprintf(&b, "complete -c beebox -n '__fish_seen_subcommand_from %s' -l %s%s\n", c.name, name, req)
		}
	}
	b.WriteString("complete -c beebox -n '__fish_seen_subcommand_from show view' -a '(beebox list --ids 2>/dev/null)'\n")
	b.WriteString("complete -c beebox -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'\n")
	b.WriteString("complete -c beebox -l plain -d 'No colour or graphics'\ncomplete -c beebox -l no-color -d 'No colour'\ncomplete -c beebox -l no-banner -d 'No banner'\n")
	return b.String()
}
