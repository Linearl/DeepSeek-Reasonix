package session

import (
	"context"
	"errors"
	"testing"
)

func TestServiceCloseAllPreservesBindingAndReleasesIdleLease(t *testing.T) {
	persistence := NewFilesystemPersistence(t.TempDir())
	service, err := NewService("local", persistence)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	runtime, err := service.Create(t.Context(), CreateOptions{SessionID: "shutdown"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := service.Bind(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CloseAll(t.Context()); !errors.Is(err, ErrRuntimeBound) {
		t.Fatalf("close bound runtime = %v", err)
	}
	if current, ok := service.Runtime(runtime.Ref()); !ok || current != runtime {
		t.Fatal("shutdown removed a bound runtime")
	}
	if err := binding.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.CloseAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	other, err := NewService("local", persistence)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.CloseAll(context.Background()) })
	reopened, err := other.EnsureExecution(t.Context(), runtime.Ref())
	if err != nil {
		t.Fatalf("writer lease was not released: %v", err)
	}
	if err := reopened.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := other.CloseAll(t.Context()); err != nil {
		t.Fatal(err)
	}
}
