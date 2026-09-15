package main

import (
	"context"
	"path/filepath"

	"reasonix/internal/session"
)

func migrateLegacy(ctx context.Context, sourcePath, targetRoot string) (session.MigrationResult, error) {
	return session.MigrateLegacy(ctx, sourcePath, targetRoot)
}

func createEmpty(ctx context.Context, targetRoot string) (string, string, error) {
	svc, err := session.NewService("sessionv4exp", session.NewFilesystemPersistence(targetRoot))
	if err != nil {
		return "", "", err
	}
	defer func() { _ = svc.CloseAll(ctx) }()
	rt, err := svc.Create(ctx, session.CreateOptions{})
	if err != nil {
		return "", "", err
	}
	ref := rt.Ref()
	dir := filepath.Join(targetRoot, ref.SessionID)
	if err := svc.Close(ctx, ref); err != nil {
		return "", "", err
	}
	return ref.SessionID, dir, nil
}
