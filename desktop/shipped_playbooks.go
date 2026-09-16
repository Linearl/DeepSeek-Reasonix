package main

import (
	"log/slog"
	"path/filepath"

	"reasonix/internal/config"
	"reasonix/internal/skill"
	"reasonix/internal/skill/builtincontent"
)

// installShippedPlaybooksToUserDir copies the three MiMo-derived playbooks into
// <reasonix-home>/skills/ on first sight (task 116). Existing files are left
// alone so user edits survive restarts. Failures are logged, never fatal: the
// embedded copies remain available either way.
func installShippedPlaybooksToUserDir() {
	dest := filepath.Join(config.ReasonixHomeDir(), skill.SkillsDirname)
	res, err := builtincontent.InstallToUserDir(dest)
	if err != nil {
		slog.Warn("desktop: shipped playbook install", "dir", dest, "err", err)
		return
	}
	if len(res.Installed) > 0 {
		slog.Info("desktop: shipped playbooks installed", "dir", dest, "installed", res.Installed)
	}
}
