# FORK-v1.31.5

## Bug Fixes

- **MiMo reasoning effort picker no longer disappears**: When switching from DeepSeek to MiMo models, the effort picker (low/medium/high) now appears correctly for the public API endpoint (`api.xiaomimimo.com`). Previously, the auto-detection path in `EffortCapabilityForEntry` had no MiMo entry, so MiMo entries without an explicit `reasoning_protocol` setting fell through to `{Supported: false}`. Enterprise MiMo endpoints (e.g. `token-plan-cn.xiaomimimo.com`) are intentionally excluded — users must configure `supported_efforts` manually for those. (#9411)
