package config

import (
	"fmt"
	"strings"
)

func renderProviderIdentity(b *strings.Builder, keyEnv, displayName string) {
	fmt.Fprintf(b, "api_key_env = %q\n", keyEnv)
	if displayName != "" {
		fmt.Fprintf(b, "display_name = %q\n", displayName)
	}
}

// renderProviderHidden writes the opt-in hidden flag. It is only emitted when set,
// so an untouched config keeps rendering exactly the lines it rendered before.
func renderProviderHidden(b *strings.Builder, hidden bool) {
	if hidden {
		b.WriteString("hidden      = true   # keep this connection out of pickers; refs still resolve\n")
	}
}
