package control

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessioninbox"
	"reasonix/internal/tool"
)

// steerImageProvider blocks the first request so the test can admit a steer
// while the turn is genuinely running, then answers every later request with
// a plain final text so each consumed steer costs exactly one extra round.
type steerImageProvider struct {
	started  chan struct{}
	release  chan struct{}
	requests []provider.Request
}

func (p *steerImageProvider) Name() string { return "steer-image" }

func (p *steerImageProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.requests = append(p.requests, req)
	ch := make(chan provider.Chunk, 2)
	if len(p.requests) == 1 {
		close(p.started)
		go func() {
			defer close(ch)
			select {
			case <-p.release:
				ch <- provider.Chunk{Type: provider.ChunkText, Text: "ready"}
				ch <- provider.Chunk{Type: provider.ChunkDone}
			case <-ctx.Done():
			}
		}()
		return ch, nil
	}
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "applied"}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

// steerImageFixture wires a controller over a workspace that holds one
// attachment image. modelRef picks the capability under test (vision-pro vs
// text-only from writeVisionTestConfig).
func steerImageFixture(t *testing.T, modelRef string) (*Controller, *steerImageProvider, *sync.Once, chan event.Event, string) {
	t.Helper()
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeVisionTestConfig(t, workspace)
	imagePath := filepath.Join(workspace, "diagram.png")
	if err := os.WriteFile(imagePath, mustBase64(t, tinyPNG), 0o644); err != nil {
		t.Fatal(err)
	}
	prov := &steerImageProvider{started: make(chan struct{}), release: make(chan struct{})}
	sess := agent.NewSession("sys")
	exec := agent.New(prov, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	sink, done, _ := collectSink()
	c := New(Options{
		Runner:        exec,
		Executor:      exec,
		Sink:          sink,
		SessionDir:    dir,
		SessionPath:   filepath.Join(dir, "s.jsonl"),
		WorkspaceRoot: workspace,
		ModelRef:      modelRef,
	})
	// Release the blocked stream on any exit path: a mid-test Fatal must not
	// leave the turn goroutine streaming forever (autosaveWG.Wait would hang
	// the whole run past go test's timeout).
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(prov.release) })
		c.autosaveWG.Wait()
	})
	return c, prov, &releaseOnce, done, imagePath
}

// TestSteerWithFrozenImagesInjectsMidTurn (任务870): with a vision-capable
// model, guidance carrying an image attachment is admitted as a mid-turn
// steer and the injected user message embeds the enqueue-time image bytes, so
// the next provider request sees them.
func TestSteerWithFrozenImagesInjectsMidTurn(t *testing.T) {
	c, prov, release, done, imagePath := steerImageFixture(t, "custom/vision-pro")

	c.Submit("initial turn")
	select {
	case <-prov.started:
	case <-time.After(10 * time.Second):
		t.Fatal("initial provider turn did not start")
	}

	rec, err := c.EnqueueInbox(InboxRequest{Intent: sessioninbox.IntentSteer, Submit: "inspect @" + imagePath})
	if err != nil {
		t.Fatal(err)
	}
	_, env, err := c.ReadInboxItem(rec.ItemID)
	if err != nil || len(env.FrozenImages) != 1 {
		t.Fatalf("frozen image envelope = %+v err=%v", env, err)
	}
	frozen := env.FrozenImages[0]

	got, err := c.TrySteerInboxItem(rec.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Disposition != sessioninbox.DispositionSteerAccepted {
		t.Fatalf("image steer disposition = %q, want steer_accepted", got.Disposition)
	}

	release.Do(func() { close(prov.release) })
	waitForDoneWithin(t, done, 30*time.Second)

	if len(prov.requests) != 2 {
		t.Fatalf("provider requests = %d, want 2 (initial + steer round)", len(prov.requests))
	}
	var steerMessage *provider.Message
	messages := prov.requests[1].Messages
	for i := range messages {
		if _, isSteer := agent.SteerText(messages[i].Content); isSteer && strings.Contains(messages[i].Content, "inspect @") {
			steerMessage = &messages[i]
			break
		}
	}
	if steerMessage == nil {
		t.Fatalf("steer message missing from the follow-up request: %+v messages", messages)
	}
	if len(steerMessage.Images) != 1 || steerMessage.Images[0] != frozen {
		t.Fatalf("steer message images = %v, want the enqueue-time data URL", steerMessage.Images)
	}
}

// TestSteerWithFrozenImagesDegradesToFollowupWithoutVision pins the
// capability gate: a non-vision model keeps the pre-870 contract — the image
// steer is converted to a queued follow-up instead of being injected
// mid-turn.
func TestSteerWithFrozenImagesDegradesToFollowupWithoutVision(t *testing.T) {
	c, prov, release, done, imagePath := steerImageFixture(t, "custom/text-only")

	c.Submit("initial turn")
	select {
	case <-prov.started:
	case <-time.After(10 * time.Second):
		t.Fatal("initial provider turn did not start")
	}

	rec, err := c.EnqueueInbox(InboxRequest{Intent: sessioninbox.IntentSteer, Submit: "inspect @" + imagePath})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.TrySteerInboxItem(rec.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Disposition != sessioninbox.DispositionQueuedFollowup {
		t.Fatalf("non-vision image steer disposition = %q, want queued_followup", got.Disposition)
	}
	meta, _, err := c.ReadInboxItem(rec.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Intent != sessioninbox.IntentFollowup {
		t.Fatalf("degraded steer intent = %q, want followup", meta.Intent)
	}

	release.Do(func() { close(prov.release) })
	waitForDoneWithin(t, done, 30*time.Second)
	for _, req := range prov.requests {
		for _, message := range req.Messages {
			if _, isSteer := agent.SteerText(message.Content); isSteer && strings.Contains(message.Content, "inspect @") {
				t.Fatalf("non-vision image steer was injected mid-turn: %+v", message)
			}
		}
	}
}
