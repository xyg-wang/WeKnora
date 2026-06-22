package docparser

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// joinedPaddlePages simulates the page-stitching logic inside callLayoutParsing.
// We can't call the real method end-to-end without an HTTP server, but the
// offset-building loop is the part we need to verify — it has the bug-prone
// "skip separator before the first non-empty page" branch.
func joinedPaddlePages(pages []string) (string, []types.PageOffset) {
	const sep = "\n\n"
	offsets := make([]types.PageOffset, 0, len(pages))
	var b strings.Builder
	for i, raw := range pages {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		offsets = append(offsets, types.PageOffset{Offset: b.Len(), Page: i + 1})
		if b.Len() > 0 {
			b.WriteString(sep)
			offsets[len(offsets)-1].Offset = b.Len()
		}
		b.WriteString(raw)
	}
	return b.String(), offsets
}

func TestPaddlePageOffsets(t *testing.T) {
	pages := []string{
		"page-one paragraph alpha",
		"page-two paragraph bravo with more text",
		"",
		"page-four after a blank page",
	}
	joined, offsets := joinedPaddlePages(pages)

	if len(offsets) != 3 {
		t.Fatalf("expected 3 non-empty page entries, got %d (%+v)", len(offsets), offsets)
	}
	if offsets[0].Page != 1 || offsets[1].Page != 2 || offsets[2].Page != 4 {
		t.Fatalf("page numbers should match source 1-based index even when a page is blank, got %+v", offsets)
	}

	// First non-empty page begins at offset 0; second begins after the first
	// page's text plus the "\n\n" separator.
	if offsets[0].Offset != 0 {
		t.Fatalf("first non-empty page should start at offset 0, got %d", offsets[0].Offset)
	}
	wantSecond := len(pages[0]) + len("\n\n")
	if offsets[1].Offset != wantSecond {
		t.Fatalf("second page offset = %d, want %d", offsets[1].Offset, wantSecond)
	}
	// Page 3 was blank so the third recorded entry is page 4. Its offset must
	// land on the start of "page-four..." inside the joined text.
	if idx := strings.Index(joined, "page-four"); idx != offsets[2].Offset {
		t.Fatalf("page 4 offset = %d, but 'page-four' lives at %d in joined", offsets[2].Offset, idx)
	}
}

func TestPaddlePageOffsetsSinglePage(t *testing.T) {
	_, offsets := joinedPaddlePages([]string{"single page body"})
	if len(offsets) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(offsets))
	}
	if offsets[0].Offset != 0 || offsets[0].Page != 1 {
		t.Fatalf("single-page offset wrong: %+v", offsets[0])
	}
}

func TestPaddlePageOffsetsAllBlank(t *testing.T) {
	joined, offsets := joinedPaddlePages([]string{"", "  ", "\t\n"})
	if joined != "" {
		t.Fatalf("expected empty joined text for all-blank pages, got %q", joined)
	}
	if len(offsets) != 0 {
		t.Fatalf("expected no offsets for all-blank pages, got %d", len(offsets))
	}
}
