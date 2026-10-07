package control

// controllerModelSettings keeps the immutable snapshot and its host admission
// callback together for the lifetime of one controller. The callback is guarded
// by Controller.mu; snapshot fields are immutable after construction.
type controllerModelSettings struct {
	revision            string
	sourceRevision      string
	current             func() (string, error)
	beforeInboxDispatch func(*Controller) (func(), error)
	// onInboxDispatchExhausted (任务579) fires without any lock held when the
	// bounded runtime-unpublished retry budget is spent, so the host can
	// surface the stuck item (desktop: a 570 delivery receipt).
	onInboxDispatchExhausted func(InboxDispatchExhausted)
}

func newControllerModelSettings(opts Options) controllerModelSettings {
	return controllerModelSettings{
		revision:                 opts.ModelSettingsRevision,
		sourceRevision:           opts.ModelSettingsSourceRevision,
		current:                  opts.ModelSettingsCurrent,
		beforeInboxDispatch:      opts.BeforeInboxDispatch,
		onInboxDispatchExhausted: opts.OnInboxDispatchExhausted,
	}
}
