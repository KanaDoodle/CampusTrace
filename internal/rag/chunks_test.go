package rag

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunkBoundariesPreserveOverlapTailAndUnicode(t *testing.T) {
	text := strings.Repeat("甲", 700) + strings.Repeat("乙", 200) + strings.Repeat("丙", 500) + "🦀"
	expected := []string{strings.Repeat("甲", 700) + strings.Repeat("乙", 200), strings.Repeat("乙", 200) + strings.Repeat("丙", 500) + "🦀", "🦀"}
	if parts := splitText(text); !reflect.DeepEqual(parts, expected) {
		t.Fatal("overlap or tail changed", parts)
	}
	for _, part := range splitText(text) {
		if !utf8.ValidString(part) {
			t.Fatal("split a Unicode character")
		}
	}
	for _, tc := range []struct {
		text string
		want int
	}{{"", 0}, {strings.Repeat("x", 700), 1}, {strings.Repeat("x", 701), 2}, {strings.Repeat("x", 1400), 2}, {strings.Repeat("x", 1401), 3}} {
		if got := len(splitText(tc.text)); got != tc.want {
			t.Fatalf("%d characters produced %d chunks, want %d", len(tc.text), got, tc.want)
		}
	}
}
