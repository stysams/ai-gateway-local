package main

import (
	"bytes"
	"image/png"
	"testing"
)

func TestEmbeddedAppIcon(t *testing.T) {
	data, err := assets.ReadFile("assets/icons/appicon.png")
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if config.Width != 1024 || config.Height != 1024 {
		t.Fatalf("icon dimensions = %dx%d, want 1024x1024", config.Width, config.Height)
	}
}
