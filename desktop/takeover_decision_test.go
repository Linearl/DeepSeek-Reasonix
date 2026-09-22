package main

import "testing"

// Task 36 Phase 1: the prompt bridge contract — the frontend's answer pairs
// with the watcher's pending reply by marker, and stale answers are inert.

func TestTakeoverDecisionPairsByMarker(t *testing.T) {
	RegisterTakeoverPromptSink(nil)
	t.Cleanup(func() { RegisterTakeoverPromptSink(nil) })

	var prompted takeoverDecisionReq
	RegisterTakeoverPromptSink(func(req takeoverDecisionReq) { prompted = req })

	reply := registerTakeoverPending("m-1")
	sink := takeoverPromptSink
	sink(takeoverDecisionReq{Marker: "m-1", Path: "x.jsonl", From: "gc-a", Reply: reply})
	if prompted.Marker != "m-1" || prompted.From != "gc-a" {
		t.Fatalf("prompt = %+v", prompted)
	}
	if !SubmitTakeoverDecision("m-1", true) {
		t.Fatal("decision for a pending marker must be accepted")
	}
	if accept := <-reply; !accept {
		t.Fatal("reply must carry the user's verdict")
	}
}

func TestTakeoverDecisionRejectAndStale(t *testing.T) {
	RegisterTakeoverPromptSink(nil)
	t.Cleanup(func() { RegisterTakeoverPromptSink(nil) })

	// Stale/double-clicked answer: no pending marker, inert.
	if SubmitTakeoverDecision("gone", false) {
		t.Fatal("stale decision must return false and change nothing")
	}

	reply := registerTakeoverPending("m-2")
	if !SubmitTakeoverDecision("m-2", false) {
		t.Fatal("reject decision must be accepted")
	}
	select {
	case accept := <-reply:
		if accept {
			t.Fatal("reply must carry false for a rejection")
		}
	default:
		t.Fatal("reply never received the rejection")
	}
	// Consumed: a second submit is stale.
	if SubmitTakeoverDecision("m-2", true) {
		t.Fatal("second decision on a consumed marker must be inert")
	}
}
