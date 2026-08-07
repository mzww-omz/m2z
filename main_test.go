package main

import "testing"

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"misskey.example":          "https://misskey.example",
		"https://misskey.example/": "https://misskey.example",
		"http://localhost:3000/":   "http://localhost:3000",
	}
	for input, want := range cases {
		got, err := normalizeHost(input)
		if err != nil || got != want {
			t.Errorf("normalizeHost(%q) = %q, %v; want %q", input, got, err, want)
		}
	}

	for _, input := range []string{"", "ftp://misskey.example", "https://user:pass@misskey.example"} {
		if _, err := normalizeHost(input); err == nil {
			t.Errorf("normalizeHost(%q) accepted invalid URL", input)
		}
	}
}

func TestStreamingURL(t *testing.T) {
	got, err := streamingURL("https://misskey.example/base", "token")
	if err != nil || got != "wss://misskey.example/base/streaming?i=token" {
		t.Fatalf("streamingURL() = %q, %v", got, err)
	}
	if channelName(0) != "homeTimeline" || channelName(1) != "localTimeline" || channelName(2) != "globalTimeline" {
		t.Fatal("unexpected timeline channel mapping")
	}
}
