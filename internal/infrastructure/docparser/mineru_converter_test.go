package docparser

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNormalizeMinerUMarkdownPreservesMarkdownAndHTML(t *testing.T) {
	input := strings.Join([]string{
		"# Heading",
		"",
		"![](images/cover.jpg)",
		"",
		`<details><summary>text_image</summary>caption</details>`,
		"",
		`<table><tr><td><img src="images/profile.jpg"/></td></tr></table>`,
	}, "\n")

	got := normalizeMinerUMarkdown(input)

	if !strings.Contains(got, "# Heading") {
		t.Fatalf("expected heading to stay intact, got: %q", got)
	}
	if strings.Contains(got, `\# Heading`) {
		t.Fatalf("expected heading to avoid escaped form, got: %q", got)
	}
	if !strings.Contains(got, "![](images/cover.jpg)") {
		t.Fatalf("expected markdown image syntax to stay intact, got: %q", got)
	}
	if strings.Contains(got, `!\[](images/cover.jpg)`) {
		t.Fatalf("expected markdown image syntax to avoid escaped form, got: %q", got)
	}
	if !strings.Contains(got, `<details><summary>text_image</summary>caption</details>`) {
		t.Fatalf("expected details/summary block to be preserved, got: %q", got)
	}
	if !strings.Contains(got, `<img src="images/profile.jpg"/>`) {
		t.Fatalf("expected html img tag to be preserved, got: %q", got)
	}
}

func TestProcessImagesKeepsReferencedVariants(t *testing.T) {
	reader := &MinerUReader{}
	mdContent := strings.Join([]string{
		"![](images/cover.jpg)",
		`<img src="./images/profile.jpg"/>`,
		`![](plain.jpg)`,
	}, "\n")

	png := createTestPNG(200, 150)
	b64 := base64.StdEncoding.EncodeToString(png)
	images := map[string]string{
		"cover.jpg":   "data:image/png;base64," + b64,
		"profile.jpg": "data:image/png;base64," + b64,
		"plain.jpg":   "data:image/png;base64," + b64,
	}

	refs, gotMarkdown := reader.processImages(mdContent, images)

	if gotMarkdown != mdContent {
		t.Fatalf("processImages should not rewrite markdown content")
	}
	if len(refs) != 3 {
		t.Fatalf("expected 3 image refs, got %d", len(refs))
	}
}

// TestProcessImagesMatchesPathsWithSpaces guards against a regression where
// MinerU image filenames containing spaces (common on Chinese documents,
// e.g. "images/第 1 页.jpg") would be silently dropped because the markdown
// regex used to extract refs disallowed whitespace inside the URL group.
func TestProcessImagesMatchesPathsWithSpaces(t *testing.T) {
	reader := &MinerUReader{}
	mdContent := "![](images/第 1 页.jpg)"

	png := createTestPNG(200, 150)
	b64 := base64.StdEncoding.EncodeToString(png)
	images := map[string]string{
		"第 1 页.jpg": "data:image/png;base64," + b64,
	}

	refs, _ := reader.processImages(mdContent, images)
	if len(refs) != 1 {
		t.Fatalf("expected 1 image ref for path with spaces, got %d", len(refs))
	}
	if refs[0].OriginalRef != "images/第 1 页.jpg" {
		t.Fatalf("unexpected OriginalRef: %q", refs[0].OriginalRef)
	}
}

func TestBuildMinerUPageOffsets(t *testing.T) {
	md := "# Title\n\nIntro on page one.\n\n## Section\n\nMore text\nstill on page one.\n\nAlpha bravo charlie on page two." +
		"\n\nDelta echo foxtrot still on page two.\n\nThe final paragraph appears on page three."
	blocks := []mineruContentBlock{
		{Type: "text", Text: "Title", TextLevel: 1, PageIdx: 0},
		{Type: "text", Text: "Intro on page one.", PageIdx: 0},
		{Type: "text", Text: "Section", TextLevel: 2, PageIdx: 0},
		{Type: "text", Text: "More text", PageIdx: 0},
		{Type: "text", Text: "Alpha bravo charlie on page two.", PageIdx: 1},
		{Type: "text", Text: "Delta echo foxtrot still on page two.", PageIdx: 1},
		{Type: "text", Text: "The final paragraph appears on page three.", PageIdx: 2},
	}

	offsets := buildMinerUPageOffsets(md, blocks)
	if len(offsets) != 3 {
		t.Fatalf("expected 3 page transitions, got %d: %+v", len(offsets), offsets)
	}

	pageForOffset := func(off int) int {
		page := 0
		for _, o := range offsets {
			if o.Offset > off {
				break
			}
			page = o.Page
		}
		return page
	}

	wantP1 := strings.Index(md, "Title")
	if pageForOffset(wantP1) != 1 {
		t.Fatalf("offset %d should be page 1, got %d", wantP1, pageForOffset(wantP1))
	}
	wantP2 := strings.Index(md, "Alpha bravo charlie")
	if pageForOffset(wantP2) != 2 {
		t.Fatalf("offset %d should be page 2, got %d", wantP2, pageForOffset(wantP2))
	}
	wantP3 := strings.Index(md, "The final paragraph")
	if pageForOffset(wantP3) != 3 {
		t.Fatalf("offset %d should be page 3, got %d", wantP3, pageForOffset(wantP3))
	}
}

func TestBuildMinerUPageOffsetsEmpty(t *testing.T) {
	if offsets := buildMinerUPageOffsets("", nil); offsets != nil {
		t.Fatalf("expected nil offsets for empty input, got %+v", offsets)
	}
	if offsets := buildMinerUPageOffsets("hello", nil); offsets != nil {
		t.Fatalf("expected nil offsets when no blocks provided, got %+v", offsets)
	}
	if offsets := buildMinerUPageOffsets("", []mineruContentBlock{{Type: "text", Text: "x"}}); offsets != nil {
		t.Fatalf("expected nil offsets when md is empty, got %+v", offsets)
	}
}

func TestTrimAnchorBoundary(t *testing.T) {
	if got := trimAnchor(""); got != "" {
		t.Fatalf("empty input should produce empty anchor, got %q", got)
	}
	if got := trimAnchor("short"); got != "short" {
		t.Fatalf("short ASCII passes through, got %q", got)
	}
	long := strings.Repeat("a", 200)
	if got := trimAnchor(long); len(got) != 80 {
		t.Fatalf("anchor should cap at 80 chars, got %d", len(got))
	}
}

func TestDecodeContentListAcceptsArray(t *testing.T) {
	raw := []byte(`[{"type":"text","text":"hi","page_idx":0},{"type":"text","text":"world","page_idx":1}]`)
	blocks := decodeContentList(raw)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if blocks[1].PageIdx != 1 || blocks[1].Text != "world" {
		t.Fatalf("second block wrong: %+v", blocks[1])
	}
}

func TestDecodeContentListAcceptsStringifiedArray(t *testing.T) {
	// MinerU sometimes double-encodes — the field is a JSON string whose
	// content is itself a JSON array. The wrapper escapes inner quotes.
	raw := []byte(`"[{\"type\":\"text\",\"text\":\"hi\",\"page_idx\":0}]"`)
	blocks := decodeContentList(raw)
	if len(blocks) != 1 || blocks[0].Text != "hi" {
		t.Fatalf("stringified form not handled, got %+v", blocks)
	}
}

func TestDecodeContentListEmptyAndJunk(t *testing.T) {
	if blocks := decodeContentList(nil); blocks != nil {
		t.Fatalf("nil raw should return nil, got %+v", blocks)
	}
	if blocks := decodeContentList([]byte(``)); blocks != nil {
		t.Fatalf("empty raw should return nil, got %+v", blocks)
	}
	if blocks := decodeContentList([]byte(`null`)); blocks != nil {
		t.Fatalf("literal null should return nil, got %+v", blocks)
	}
	if blocks := decodeContentList([]byte(`12345`)); blocks != nil {
		t.Fatalf("non-array non-string should return nil, got %+v", blocks)
	}
}
