package control

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"reasonix/internal/baseproc"
	"reasonix/internal/tool"
)

// baseCatalogTimeout bounds one remote tool-catalog round trip. The channel is
// local stdio (milliseconds in practice); the bound only exists so a wedged
// subprocess cannot stall a turn that consults the catalog.
const baseCatalogTimeout = 5 * time.Second

// baseCatalogEntries is the S1b tool-catalog consumption gate (design §10):
//
//   - no base client wired, or inline mode (switch off / spawn fallback) →
//     (nil,false): the caller walks the pre-S1 registry path, byte for byte;
//   - remote with the tools capability → base.toolCatalog over IPC;
//   - remote without the capability (ErrNotWired, no round trip spent) or any
//     transport error → local path again (R1: fallback is the default path).
//
// The switch-off guarantee is structural: boot hands a controller the client
// baseproc.Start returned, and with experimental_base_process=false that is an
// inline client — this gate can only ever take the local branch.
func (c *Controller) baseCatalogEntries(providerVisible bool) ([]tool.ContractEntry, bool) {
	if c == nil || c.baseClient == nil || c.baseClient.Mode() != baseproc.ModeRemote {
		return nil, false
	}
	scope := baseproc.ScopeAll
	if providerVisible {
		scope = baseproc.ScopeProvider
	}
	ctx, cancel := context.WithTimeout(context.Background(), baseCatalogTimeout)
	defer cancel()
	res, err := c.baseClient.ToolCatalog(ctx, baseproc.ToolCatalogParams{Scope: scope})
	if err != nil {
		if !errors.Is(err, baseproc.ErrNotWired) {
			// Capability absence is the current normal state (quiet fallback);
			// a transport failure deserves a greppable warn (decision D5's
			// "boot: base fallback" family) so an installation can see it.
			slog.Warn("boot: base tool catalog fallback", "scope", scope, "reason", err.Error())
		}
		return nil, false
	}
	entries := make([]tool.ContractEntry, 0, len(res.Tools))
	for _, t := range res.Tools {
		entries = append(entries, tool.ContractEntry{
			Name:        t.Name,
			Description: t.Description,
			ReadOnly:    t.ReadOnly,
			Schema:      t.Schema,
		})
	}
	return entries, true
}
