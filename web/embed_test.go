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
}
