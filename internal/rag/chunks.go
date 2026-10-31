package rag

const chunkWindow = 900
const chunkStride = 700

// Keep ingestion, capacity accounting and evaluation on the same Unicode
// boundaries. IDs and persistence belong to the caller.
func splitText(text string) []string {
	runes := []rune(text)
	parts := []string{}
	for start := 0; start < len(runes); start += chunkStride {
		parts = append(parts, string(runes[start:min(start+chunkWindow, len(runes))]))
	}
	return parts
}
