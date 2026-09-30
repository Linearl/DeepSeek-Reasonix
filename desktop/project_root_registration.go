package main

// registerProjectRoot indexes workspaceRoot, realigns open tabs to its
// canonical spelling, and discovers existing sessions once per process.
func (a *App) registerProjectRoot(workspaceRoot string) {
	// Task 186: the host's own directories are not projects. Registering one
	// would be dropped by the next save (stripBuiltinProjects) while still
	// pointing the catalog reconcile at a project scope the sidebar cannot
	// render — skip the whole registration instead.
	if isBuiltinWorkspaceRoot(workspaceRoot) {
		return
	}
	_ = addProject(workspaceRoot, "")
	a.syncTabWorkspaceRootSpellings()
	root := normalizeProjectRoot(workspaceRoot)
	if root == "" {
		return
	}
	registrationKey := projectRootKey(root)
	if _, loaded := a.catalogRegisteredProjectRoots.LoadOrStore(registrationKey, struct{}{}); loaded {
		return
	}
	if !a.requestSessionCatalogReconcile(desktopSessionDir(root)) {
		a.catalogRegisteredProjectRoots.Delete(registrationKey)
	}
}
