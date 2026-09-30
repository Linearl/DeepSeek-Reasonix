package control

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/event"
)

func TestSideQueryTranslateUsesBoundedNoToolRequest(t *testing.T) {
	prov := &sessionTitleProviderStub{out: "这是译文"}
	ctrl := sessionTitleTestController(prov, event.Discard)
	out, err := ctrl.SideQuery(context.Background(), SideQueryTranslate, "hello world", "surrounding text")
	if err != nil {
		t.Fatalf("SideQuery: %v", err)
	}
	if out != "这是译文" {
		t.Fatalf("out = %q", out)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("requests = %d", len(prov.requests))
	}
	req := prov.requests[0]
	if len(req.Tools) != 0 || len(req.Messages) != 2 {
		t.Fatalf("request = %+v", req)
	}
	if !strings.Contains(req.Messages[1].Content, "hello world") || !strings.Contains(req.Messages[1].Content, "surrounding text") {
		t.Fatalf("user text missing selection/context: %q", req.Messages[1].Content)
	}
}

func TestSideQueryExplainPromptDiffersFromTranslate(t *testing.T) {
	prov := &sessionTitleProviderStub{out: "ok"}
	ctrl := sessionTitleTestController(prov, event.Discard)
	if _, err := ctrl.SideQuery(context.Background(), SideQueryExplain, "x", ""); err != nil {
		t.Fatalf("explain: %v", err)
	}
	sysExplain := prov.requests[0].Messages[0].Content
	if _, err := ctrl.SideQuery(context.Background(), SideQueryTranslate, "x", ""); err != nil {
		t.Fatalf("translate: %v", err)
	}
	sysTranslate := prov.requests[1].Messages[0].Content
	if sysExplain == sysTranslate {
		t.Fatal("explain and translate must use distinct system prompts")
	}
}

func TestSideQueryBoundsSelectionAndContext(t *testing.T) {
	prov := &sessionTitleProviderStub{out: "ok"}
	ctrl := sessionTitleTestController(prov, event.Discard)
	big := strings.Repeat("字", sideQueryMaxTextRunes+500)
	bigCtx := strings.Repeat("境", sideQueryMaxContextRunes+500)
	if _, err := ctrl.SideQuery(context.Background(), SideQueryTranslate, big, bigCtx); err != nil {
		t.Fatalf("SideQuery: %v", err)
	}
	user := prov.requests[0].Messages[1].Content
	if strings.Count(user, "字") > sideQueryMaxTextRunes {
		t.Fatalf("selection cap exceeded: %d", strings.Count(user, "字"))
	}
	if strings.Count(user, "境") > sideQueryMaxContextRunes {
		t.Fatalf("context cap exceeded: %d", strings.Count(user, "境"))
	}
}

func TestSideQueryRejectsEmptySelectionUnknownKindAndMissingProvider(t *testing.T) {
	ctrl := sessionTitleTestController(nil, event.Discard)
	if _, err := ctrl.SideQuery(context.Background(), SideQueryTranslate, "  ", ""); err == nil || !strings.Contains(err.Error(), "empty selection") {
		t.Fatalf("empty selection err = %v", err)
	}
	if _, err := ctrl.SideQuery(context.Background(), "nope", "text", ""); err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("unknown kind err = %v", err)
	}
	provCtrl := sessionTitleTestController(&sessionTitleProviderStub{err: errors.New("boom")}, event.Discard)
	if _, err := provCtrl.SideQuery(context.Background(), SideQueryExplain, "text", ""); err == nil {
		t.Fatal("provider failure must surface")
	}
}
