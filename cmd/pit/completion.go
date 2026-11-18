package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/patrickbergner/pit/internal/workspace"
)

// The completion scripts are thin: they hand the command line to pit __complete, which knows pit's commands, options, repos and groups.
// The word being completed goes first, with a leading "_", as Windows PowerShell drops an empty argument.
var completionScripts = map[string]string{
	"bash": `# bash completion for pit; load it with: source <(pit completion bash)
_pit() {
    local IFS=$'\n'
    COMPREPLY=($(pit __complete "_${COMP_WORDS[COMP_CWORD]}" "${COMP_WORDS[@]:1:COMP_CWORD-1}" 2>/dev/null))
    if [[ ${#COMPREPLY[@]} -eq 1 && ${COMPREPLY[0]} == *= ]]; then
        compopt -o nospace
    fi
}
complete -o default -F _pit pit
`,
	"powershell": `# PowerShell completion for pit; load it with: pit completion powershell | Out-String | Invoke-Expression
Register-ArgumentCompleter -Native -CommandName pit, pit.exe -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $before = @($commandAst.CommandElements | Select-Object -Skip 1 |
        Where-Object { $_.Extent.EndOffset -lt $cursorPosition } | ForEach-Object { $_.Extent.Text })
    pit __complete "_$wordToComplete" @before 2>$null | ForEach-Object {
        [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
    }
}
`,
}

// commandOptions are the options completion offers after each command: pit's own and the git options most used with it.
var commandOptions = map[string][]string{
	"sync":    nil,
	"list":    nil,
	"tree":    nil,
	"status":  {"--fetch"},
	"diff":    {"--staged", "--cached", "--stat", "--name-only", "--name-status", "--exit-code", "--quiet"},
	"grep":    {"--ignore-case", "--word-regexp", "--line-number", "--files-with-matches", "--count", "--fixed-strings", "--untracked"},
	"add":     {"--all", "--update", "--patch", "--interactive", "--force", "--dry-run"},
	"restore": {"--staged", "--worktree", "--patch", "--source="},
	"rm":      {"--recursive", "--cached", "--force", "--dry-run", "--quiet"},
	"mv":      {"--force", "--dry-run", "--verbose"},
	"commit":  {"--all", "--message", "--file", "--no-verify", "--continue", "--abort", "--amend", "--no-edit"},
	"revert":  {"--message", "--file", "--no-verify", "--no-edit"},
	"stash":   {"--message", "--include-untracked", "--keep-index", "--patch", "--index", "--quiet"},
	"log": {"--oneline", "--max-count", "--author=", "--committer=", "--since=", "--until=", "--grep=", "--no-merges",
		"--first-parent"},
	"show":    {"--stat", "--name-only", "--name-status"},
	"graph":   {"--all", "--oneline", "--format=", "--max-count", "--author=", "--since=", "--first-parent", "--simplify-by-decoration"},
	"changes": {"--stat", "--name-only", "--numstat", "--patch", "--diff-filter="},
	"fetch":   {"--prune", "--tags", "--all"},
	"pull":    {"--rebase", "--ff-only", "--no-rebase"},
	"push":    {"--dry-run", "--interactive", "--yes", "--force-with-lease"},
	"tag":     nil,
	"pushtag": {"--interactive", "--yes"},
	"each":    nil,
}

func cmdCompletion(args []string) error {
	if len(args) != 1 || completionScripts[args[0]] == "" {
		return errors.New("completion: give the shell: bash or powershell")
	}
	fmt.Print(completionScripts[args[0]])
	return nil
}

// complete prints the completions of the word being typed, one per line: pit __complete _<word> [<words before it>...]. Printing nothing
// leaves the completion of file names to the shell.
func complete(args []string) {
	if len(args) == 0 {
		return
	}
	var names, groups []string
	if cwd, err := os.Getwd(); err == nil {
		if w, err := workspace.Open(cwd); err == nil {
			for _, r := range w.All() {
				names = append(names, r.Name)
			}
			groups = w.Config.Groups()
		}
	}
	for _, c := range completions(strings.TrimPrefix(args[0], "_"), args[1:], names, groups) {
		fmt.Println(c)
	}
}

// completions returns the candidates for the word cur, given the words before it (without "pit") and the root's repos and groups.
func completions(cur string, before, names, groups []string) []string {
	if slices.Contains(before, "--") {
		return nil
	}
	cmd, afterCmd := "", []string(nil)
	for i, b := range before {
		if !strings.HasPrefix(b, "-") {
			cmd, afterCmd = b, before[i+1:]
			break
		}
	}
	prev := ""
	if len(before) > 0 {
		prev = before[len(before)-1]
	}
	var candidates []string
	switch {
	case prev == "-r" || prev == "--repo":
		candidates = names
	case prev == "-g" || prev == "--group":
		candidates = groups
	case strings.HasPrefix(cur, "--color="):
		candidates = []string{"--color=always", "--color=never", "--color=auto"}
	case cmd == "" && strings.HasPrefix(cur, "-"):
		candidates = []string{"--color", "--no-color", "--no-pager", "--help", "--version"}
	case cmd == "":
		candidates = append(slices.Collect(maps.Keys(commands)), "completion", "help", "version")
	case strings.HasPrefix(cur, "-"):
		candidates = append(slices.Clone(commandOptions[cmd]), "--repo", "--group", "--color", "--no-color", "--help")
		if commands[cmd].pages {
			candidates = append(candidates, "--no-pager")
		}
	case cmd == "stash" && len(afterCmd) == 0:
		candidates = []string{"push", "pop", "apply", "drop", "list"}
	case cmd == "completion" && len(afterCmd) == 0:
		candidates = slices.Collect(maps.Keys(completionScripts))
	}
	var out []string
	for _, c := range candidates {
		if strings.HasPrefix(c, cur) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out
}
