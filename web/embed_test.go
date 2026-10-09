package web

import (
	"regexp"
	"strings"
	"testing"
)

func TestPageAssetsAreIncludedInBinary(t *testing.T) {
	page, err := Files.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range regexp.MustCompile(`(?:src|href)="(/[^"#?]+)"`).FindAllStringSubmatch(string(page), -1) {
		name := strings.TrimPrefix(match[1], "/")
		if data, err := Files.ReadFile(name); err != nil || len(data) == 0 {
			t.Fatalf("page asset %q is unavailable in compiled server: %v", name, err)
		}
	}
	loader, err := Files.ReadFile("loader.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range regexp.MustCompile(`'([a-zA-Z0-9_/-]+\.(?:js|css))'`).FindAllStringSubmatch(string(loader), -1) {
		if data, err := Files.ReadFile(match[1]); err != nil || len(data) == 0 {
			t.Fatalf("lazy asset %q missing from binary: %v", match[1], err)
		}
	}
}
