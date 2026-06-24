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

func TestPageNosForRange(t *testing.T) {
	offsets := []types.PageOffset{
		{Offset: 0, Page: 1},
		{Offset: 4000, Page: 2},
		{Offset: 9000, Page: 3},
	}

	cases := []struct {
		name       string
		start, end int
		want       []int
	}{
		{"within one page", 100, 300, []int{1}},
		{"crosses into page 2", 3900, 4100, []int{1, 2}},
		{"starts at page 2", 4000, 4100, []int{2}},
		{"ends exactly at page 2", 3900, 4000, []int{1}},
		{"crosses two page boundaries", 3900, 9100, []int{1, 2, 3}},
		{"beyond last boundary", 9999, 11000, []int{3}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pageNosForRange(offsets, tc.start, tc.end)
			if len(got) != len(tc.want) {
				t.Fatalf("pageNosForRange(%d,%d) len = %d, want %d (%v)", tc.start, tc.end, len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("pageNosForRange(%d,%d) = %v, want %v", tc.start, tc.end, got, tc.want)
				}
			}
		})
	}

	if got := pageNosForRange(nil, 100, 200); got != nil {
		t.Fatalf("nil offsets should yield nil, got %v", got)
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

func TestPageMetadataFromChunkMetadata(t *testing.T) {
	mkJSON := func(m map[string]any) types.JSON {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return types.JSON(b)
	}

	cases := []struct {
		name     string
		meta     types.JSON
		wantNo   int
		wantList []int
	}{
		{"legacy scalar only", mkJSON(map[string]any{"page_no": "5"}), 5, []int{5}},
		{"array with scalar", mkJSON(map[string]any{"page_no": "5", "page_nos": []int{5, 6}}), 5, []int{5, 6}},
		{"array only", mkJSON(map[string]any{"page_nos": []any{float64(7), float64(8)}}), 7, []int{7, 8}},
		{"dedupe invalid", mkJSON(map[string]any{"page_nos": []any{float64(3), float64(3), "bad", float64(0)}}), 3, []int{3}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotNo, gotList := types.PageMetadataFromChunkMetadata(tc.meta)
			if gotNo != tc.wantNo {
				t.Fatalf("page no = %d, want %d", gotNo, tc.wantNo)
			}
			if len(gotList) != len(tc.wantList) {
				t.Fatalf("page list = %v, want %v", gotList, tc.wantList)
			}
			for i := range gotList {
				if gotList[i] != tc.wantList[i] {
					t.Fatalf("page list = %v, want %v", gotList, tc.wantList)
				}
			}
		})
	}
}
