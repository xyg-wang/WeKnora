package service

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestPageNoForOffset(t *testing.T) {
	offsets := []types.PageOffset{
		{Offset: 0, Page: 1},
		{Offset: 4000, Page: 2},
		{Offset: 9000, Page: 3},
	}

	cases := []struct {
		name string
		off  int
		want int
	}{
		{"start of page 1", 0, 1},
		{"middle of page 1", 100, 1},
		{"end of page 1", 3999, 1},
		{"start of page 2", 4000, 2},
		{"middle of page 2", 5000, 2},
		{"start of page 3", 9000, 3},
		{"beyond last boundary", 999999, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pageNoForOffset(offsets, tc.off); got != tc.want {
				t.Fatalf("pageNoForOffset(%d) = %d, want %d", tc.off, got, tc.want)
			}
		})
	}

	if got := pageNoForOffset(nil, 100); got != 0 {
		t.Fatalf("nil offsets should yield 0, got %d", got)
	}
}

func TestPageNoFromChunkMetadata(t *testing.T) {
	mkJSON := func(m map[string]any) types.JSON {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return types.JSON(b)
	}

	cases := []struct {
		name string
		meta types.JSON
		want int
	}{
		{"nil metadata", nil, 0},
		{"empty metadata", types.JSON("{}"), 0},
		{"string page", mkJSON(map[string]any{"page_no": "5"}), 5},
		{"number page", mkJSON(map[string]any{"page_no": 7}), 7},
		{"missing key", mkJSON(map[string]any{"other": "x"}), 0},
		{"non-numeric string", mkJSON(map[string]any{"page_no": "abc"}), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pageNoFromChunkMetadata(tc.meta); got != tc.want {
				t.Fatalf("pageNoFromChunkMetadata = %d, want %d", got, tc.want)
			}
		})
	}
}
