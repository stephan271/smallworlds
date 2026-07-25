package main

import (
	"reflect"
	"testing"
)

func TestBrowserCommandUsesOnlySupportedPlatformAdapters(t *testing.T) {
	url := "http://127.0.0.1:4174/?token=single-use"
	for platform, want := range map[string][]string{
		"linux":   {"xdg-open", url},
		"darwin":  {"open", url},
		"windows": {"rundll32", "url.dll,FileProtocolHandler", url},
	} {
		command, err := browserCommand(platform, url)
		if err != nil {
			t.Fatalf("browser command for %s: %v", platform, err)
		}
		if !reflect.DeepEqual(command.Args, want) {
			t.Fatalf("browser command for %s = %#v, want %#v", platform, command.Args, want)
		}
	}
	if _, err := browserCommand("plan9", url); err == nil {
		t.Fatal("unsupported platforms must not silently choose a browser command")
	}
}
