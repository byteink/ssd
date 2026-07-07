// Package completion provides shell completion scripts and dynamic
// completion candidates for the ssd CLI.
//
// The shell scripts (bash/zsh/fish) are tiny shims that delegate to
// `ssd __complete <prev-tokens>` for every TAB press. The hidden
// __complete command parses the tokens already on the command line
// and prints newline-separated candidates on stdout; the shell does
// the final prefix filtering against the partial word at the cursor.
//
// Keeping the scripts thin means there is one source of truth for the
// command/sub-command layout (this package, in Go) instead of three
// copies that drift apart per shell.
package completion

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// TopLevelCommands lists every command users can invoke directly via
// `ssd <command>`. Hidden helpers (e.g. __complete) are intentionally
// omitted so they do not surface in TAB completion.
var TopLevelCommands = []string{
	"init", "migrate", "deploy", "up", "update", "down", "rm", "restart",
	"rollback", "status", "logs", "config", "env", "secret",
	"prune", "scale", "provision", "skill", "completion",
	"version", "help",
}

// ServiceCommands take a single optional service positional argument
// (or none, meaning "all services"). Completion offers the configured
// service names from ssd.yaml.
var ServiceCommands = map[string]bool{
	"deploy": true, "up": true, "update": true, "down": true, "rm": true,
	"restart": true, "rollback": true, "status": true,
	"logs": true, "config": true, "scale": true,
}

// envSecretSubcommands are the action verbs accepted by `ssd env` and
// `ssd secret` after the service name.
var envSecretSubcommands = []string{"set", "list", "rm"}

// provisionSubcommands lists optional sub-actions for `ssd provision`.
var provisionSubcommands = []string{"check"}

// completionShells lists the shells we ship completion scripts for.
var completionShells = []string{"bash", "zsh", "fish"}

// pruneFlags lists the flags `ssd prune` accepts. Used for completion.
var pruneFlags = []string{
	"--dry-run", "--images", "--build-cache",
	"--dangling", "--all", "--keep",
}

// globalFlags are flags accepted on every command. We have to skip
// them (and any value they consume) when scanning the prev-tokens for
// the first positional argument.
var globalFlagsWithValue = map[string]bool{
	"--config": true,
	"--env":    true,
	"-e":       true,
}

// Complete prints completion candidates for the given list of
// already-typed tokens (NOT including the binary name and NOT
// including the partial word at the cursor). One candidate per line.
//
// The shell filters the output against the cursor's current prefix,
// so this function returns ALL plausible candidates at the position
// it infers from prev. It never returns an error: any I/O failure
// (e.g. missing ssd.yaml) collapses to "no candidates", which is the
// only sensible behavior during a TAB press.
func Complete(prev []string, services []string) {
	cmd, postCmd := splitCommand(prev)

	if cmd == "" {
		// Completing the command name itself.
		printLines(TopLevelCommands)
		return
	}

	switch {
	case cmd == "completion":
		completeCompletion(postCmd)
	case cmd == "env" || cmd == "secret":
		completeEnvSecret(postCmd, services)
	case cmd == "provision":
		completeProvision(postCmd)
	case cmd == "prune":
		printLines(pruneFlags)
	case ServiceCommands[cmd]:
		// One positional argument max; once a service has been picked,
		// stop offering more service names.
		if firstPositional(postCmd) == "" {
			printLines(services)
		}
	}
}

func completeCompletion(postCmd []string) {
	switch firstPositional(postCmd) {
	case "":
		out := append([]string{}, completionShells...)
		out = append(out, "install")
		printLines(out)
	case "install":
		// `--shell <name>` is the only flag.
		if !hasFlag(postCmd, "--shell") {
			printLines([]string{"--shell"})
		}
	}
}

func completeEnvSecret(postCmd []string, services []string) {
	positional := positionalArgs(postCmd)
	switch len(positional) {
	case 0:
		printLines(services)
	case 1:
		printLines(envSecretSubcommands)
	}
}

func completeProvision(postCmd []string) {
	if firstPositional(postCmd) == "" {
		printLines(provisionSubcommands)
	}
}

// splitCommand returns the first non-flag token (the command) and the
// tokens that follow it. Global flags (--config <v>, --env <v>, -e <v>)
// are skipped along with their value.
func splitCommand(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if globalFlagsWithValue[a] {
			i++ // skip the value too
			continue
		}
		if strings.HasPrefix(a, "--config=") || strings.HasPrefix(a, "--env=") {
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a, args[i+1:]
	}
	return "", nil
}

// firstPositional returns the first non-flag token in args, or "" if
// there is none.
func firstPositional(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if globalFlagsWithValue[a] {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

// positionalArgs returns all non-flag tokens in args, in order.
func positionalArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if globalFlagsWithValue[a] {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}

func printLines(items []string) {
	for _, s := range items {
		fmt.Println(s)
	}
}

// Script returns the completion script for the given shell. Returns
// an error for unknown shells.
func Script(shell string) (string, error) {
	switch shell {
	case "bash":
		return bashScript, nil
	case "zsh":
		return zshScript, nil
	case "fish":
		return fishScript, nil
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish)", shell)
	}
}

// InstallPath returns the absolute path the completion script for
// shell will be written to by Install. Exported so callers (tests,
// help text) can show it without writing anything.
func InstallPath(shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home directory: %w", err)
	}
	switch shell {
	case "bash":
		return filepath.Join(home, ".local", "share", "bash-completion", "completions", "ssd"), nil
	case "zsh":
		return filepath.Join(home, ".zsh", "completions", "_ssd"), nil
	case "fish":
		return filepath.Join(home, ".config", "fish", "completions", "ssd.fish"), nil
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish)", shell)
	}
}

// ActivationHint returns shell-specific instructions the user needs
// to follow once for the completion file to be picked up. Returns ""
// when no action is needed (e.g. fish auto-loads its completions dir).
func ActivationHint(shell, path string) string {
	switch shell {
	case "bash":
		return "Ensure bash-completion is installed (brew install bash-completion@2 on macOS;\n" +
			"  apt install bash-completion / dnf install bash-completion on Linux),\n" +
			"  then open a new shell."
	case "zsh":
		return "Add the completions directory to fpath in ~/.zshrc (before `compinit`):\n" +
			"    fpath=(" + filepath.Dir(path) + " $fpath)\n" +
			"    autoload -Uz compinit && compinit\n" +
			"  Then open a new shell."
	case "fish":
		return "Open a new shell (fish auto-loads completions from " + filepath.Dir(path) + ")."
	default:
		return ""
	}
}

// Install writes the completion script for shell to its conventional
// per-user path and returns the path it wrote to. Existing files are
// overwritten (re-installing on upgrade should be a no-op).
func Install(shell string) (string, error) {
	script, err := Script(shell)
	if err != nil {
		return "", err
	}
	path, err := InstallPath(shell)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create completion dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		return "", fmt.Errorf("write completion file: %w", err)
	}
	return path, nil
}

// DetectShell returns the user's shell ("bash", "zsh", or "fish")
// based on $SHELL, or "" if unrecognised. On Windows we always
// return "" since none of the supported shells are first-class there.
func DetectShell() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	s := os.Getenv("SHELL")
	if s == "" {
		return ""
	}
	base := filepath.Base(s)
	switch base {
	case "bash", "zsh", "fish":
		return base
	default:
		return ""
	}
}

// SupportedShells returns the list of shells Install/Script accept,
// in a stable order suitable for help text.
func SupportedShells() []string {
	return append([]string{}, completionShells...)
}

// --- shell scripts ---------------------------------------------------
//
// Each script is intentionally short. The contract is:
//   1. Capture the tokens already typed BEFORE the cursor, minus the
//      binary name itself.
//   2. Invoke `ssd __complete <those-tokens>` and read newline-
//      separated candidates from stdout.
//   3. Let the shell's native completion machinery filter those
//      candidates against the partial word at the cursor.

const bashScript = `# ssd bash completion
_ssd_completion() {
    local cur prev_words candidates prefix
    cur="${COMP_WORDS[COMP_CWORD]}"
    # Tokens between the binary name and the cursor (exclusive of cur).
    prev_words=("${COMP_WORDS[@]:1:COMP_CWORD-1}")
    candidates="$(ssd __complete "${prev_words[@]}" 2>/dev/null)"
    # Comma-separated lists (e.g. deploy web,api): complete the item after the
    # last comma and re-attach the already-typed "web," prefix to each match.
    if [[ "${cur}" == *,* ]]; then
        prefix="${cur%,*},"
        COMPREPLY=( $(compgen -W "${candidates}" -P "${prefix}" -- "${cur##*,}") )
    else
        COMPREPLY=( $(compgen -W "${candidates}" -- "${cur}") )
    fi
}
complete -F _ssd_completion ssd
`

const zshScript = `#compdef ssd

_ssd() {
    local -a prev_words candidates
    local IFS=$'\n'
    # words[1] is "ssd"; CURRENT is the index of the word at the cursor.
    if (( CURRENT > 2 )); then
        prev_words=("${(@)words[2,CURRENT-1]}")
    else
        prev_words=()
    fi
    candidates=( ${(f)"$(ssd __complete "${prev_words[@]}" 2>/dev/null)"} )
    # Comma-separated lists (e.g. deploy web,api): consume text up to the last
    # comma as a fixed prefix so matching applies to the item after it.
    compset -P '*,'
    compadd -a candidates
}

compdef _ssd ssd
`

const fishScript = `# ssd fish completion
function __ssd_complete
    set -l tokens (commandline -opc)
    set -l cur (commandline -ct)
    set -l cands
    if test (count $tokens) -gt 1
        set cands (ssd __complete $tokens[2..-1] 2>/dev/null)
    else
        set cands (ssd __complete 2>/dev/null)
    end
    # Comma-separated lists (e.g. deploy web,api): fish filters against the whole
    # token, so emit each candidate with the already-typed "web," prefix.
    if string match -q '*,*' -- $cur
        set -l prefix (string replace -r '[^,]*$' '' -- $cur)
        printf '%s\n' $prefix$cands
    else
        printf '%s\n' $cands
    end
end

complete -c ssd -f -a "(__ssd_complete)"
`
