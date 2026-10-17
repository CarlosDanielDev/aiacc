package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// shell-init is retired: launchers replaced the eval'd functions in v0.7 (ADR
// 0008, 0010). The command stays so an rc file that still evals it keeps working.
// It prints comment lines only, which every supported shell ignores.
func newShellInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "shell-init <bash|zsh|fish>",
		Short:  "Retired: prints a comment pointing at aiacc setup",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(cmd.OutOrStdout(), retiredShellInit(args[0]))
			return nil
		},
	}
}

// retiredShellInit is what `eval "$(aiacc shell-init <shell>)"` runs in a startup
// file. It must stay valid in bash, zsh and fish: a comment, then the one-time
// erase of the pre-v0.7 `aiacc` hook function in a live shell (ADR 0010).
func retiredShellInit(shellName string) string {
	return "# aiacc: shell-init is retired. Run `aiacc setup`, then delete this line from your startup file.\n" +
		eraseOldHook(shellName)
}

// eraseOldHook removes the pre-v0.7 `aiacc` shell function if it is defined, so a
// re-source of the stub un-breaks a shell that loaded the old hook. It is a no-op
// when the function is absent, and empty for shells without that hook.
func eraseOldHook(shellName string) string {
	switch shellName {
	case "fish":
		return "if functions -q aiacc; functions -e aiacc; end\n"
	case "bash", "zsh":
		return "unset -f aiacc 2>/dev/null || true\n"
	default:
		return ""
	}
}
