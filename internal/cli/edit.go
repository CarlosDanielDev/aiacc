package cli

import (
	"fmt"

	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
)

// runEditTUI is the picker's `e`: the edit screen for an account's settings file
// (endpoint, model and key). An account with no settings file, or one that can't
// be edited, gets a message screen instead of the edit screen.
func runEditTUI(row tui.Row) error {
	if row.Settings == "" {
		return showMessage("aiacc — edit profile", []tui.Line{{
			Text:  row.Account + " has no settings file to edit. Endpoint accounts (add one with settings: glm) have one.",
			Color: tui.Grey,
		}})
	}
	env, err := provider.ReadSettingsEnv(row.Settings)
	if err != nil {
		// Any read failure is shown, not returned: an error would exit the picker.
		return showMessage("aiacc — edit profile", []tui.Line{{
			Text:  fmt.Sprintf("%v — nothing to edit", err),
			Color: tui.Red,
		}})
	}
	_, err = tui.RunEdit(row.Account, row.Settings, tui.EditForm{
		BaseURL:  env.BaseURL,
		Model:    env.Model,
		HasToken: env.HasToken,
	})
	return err
}
