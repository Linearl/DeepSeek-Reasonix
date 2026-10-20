package provider

import (
	"strings"
	"testing"
)

func TestParseAudioDataURL(t *testing.T) {
	cases := []struct {
		in     string
		mime   string
		format string
		ok     bool
	}{
		{"data:audio/mpeg;base64,SGVsbG8=", "audio/mpeg", "mp3", true},
		{"data:audio/wav;base64,SGVsbG8=", "audio/wav", "wav", true},
		{"data:audio/x-flac;base64,SGVsbG8=", "audio/x-flac", "flac", true},
		{"data:audio/mp4;base64,SGVsbG8=", "audio/mp4", "m4a", true},
		{"data:audio/ogg;base64,SGVsbG8=", "audio/ogg", "ogg", true},
		{"data:application/ogg;base64,SGVsbG8=", "application/ogg", "ogg", true},
		{"data:image/png;base64,AAAA", "", "", false},
		{"data:audio/mpeg;base64,!!!not-base64!!!", "", "", false},
		{"data:audio/mpeg,raw-no-marker", "", "", false},
		{"https://example.com/a.mp3", "", "", false},
	}
	for _, tc := range cases {
		mime, payload, ok := ParseAudioDataURL(tc.in)
		if ok != tc.ok {
			t.Errorf("ParseAudioDataURL(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if mime != tc.mime || AudioWireFormat(mime) != tc.format {
			t.Errorf("ParseAudioDataURL(%q) = (%q -> %q), want (%q -> %q)", tc.in, mime, AudioWireFormat(mime), tc.mime, tc.format)
		}
		if payload == "" {
			t.Errorf("ParseAudioDataURL(%q) payload empty", tc.in)
		}
	}
}

func TestIsAudioAttachmentPath(t *testing.T) {
	accepted := []string{".reasonix/attachments/a.mp3", "clip.WAV", "x/y/z.FlAc", "a.m4a", "b.ogg", "c.mp3"}
	for _, p := range accepted {
		if !IsAudioAttachmentPath(p) {
			t.Errorf("IsAudioAttachmentPath(%q) = false, want true", p)
		}
	}
	rejected := []string{"a.mp4", "a.txt", "a.flacx", "mp3", "a.ogg.txt", ""}
	for _, p := range rejected {
		if IsAudioAttachmentPath(p) {
			t.Errorf("IsAudioAttachmentPath(%q) = true, want false", p)
		}
	}
}

func TestRejectAudioIfPresent(t *testing.T) {
	clean := []Message{{Role: RoleUser, Content: "hi"}}
	if err := RejectAudioIfPresent("openai", "m", "why", clean); err != nil {
		t.Fatalf("audio-free history must pass: %v", err)
	}
	withAudio := []Message{{Role: RoleUser, Content: "hi", Audio: []string{"data:audio/mpeg;base64,SGVsbG8="}}}
	if err := RejectAudioIfPresent("openai", "m", "why", withAudio); err == nil {
		t.Fatal("audio-carrying history must be rejected")
	} else if !strings.Contains(err.Error(), "why") || !strings.Contains(err.Error(), `"m"`) {
		t.Errorf("error should name enablement and model: %v", err)
	}
}

func TestHasAudioInput(t *testing.T) {
	if HasAudioInput([]Message{{Role: RoleUser, Content: "hi"}}) {
		t.Fatal("empty audio field must not count as audio input")
	}
	if !HasAudioInput([]Message{{Role: RoleUser, Content: "hi", Audio: []string{"data:audio/mpeg;base64,SGVsbG8="}}}) {
		t.Fatal("audio field must count as audio input")
	}
}
