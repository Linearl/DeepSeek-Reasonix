package boot

import "reasonix/internal/config"

func runtimeModelSettingsReader(root, modelName, modelRef string, settings *config.ModelRuntimeSettings) func() (string, error) {
	return func() (string, error) {
		// tl1654 (round 3): the runtime snapshot no longer re-creates a deleted
		// provider for the session's model ref — a session whose provider was
		// removed runs on the default model instead.
		current, err := config.LoadModelRuntimeSnapshot(root)
		if err != nil {
			return "", err
		}
		if err := settings.Apply(current, root); err != nil {
			return "", err
		}
		return current.ModelRuntimeFingerprint(modelRef), nil
	}
}

func runtimeImageCapabilityReader(root, modelName, snapshot string, settings *config.ModelRuntimeSettings) func() bool {
	return func() bool {
		current, err := config.LoadForRootReadOnly(root)
		if err == nil {
			err = settings.Apply(current, root)
		}
		if err != nil {
			return false
		}
		return config.ModelCapabilitySnapshot(current, config.NewModelCapabilityResolver()) != snapshot
	}
}
