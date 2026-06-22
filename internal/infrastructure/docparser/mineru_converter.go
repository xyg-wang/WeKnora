package docparser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const mineruTimeout = 1000 * time.Second // large docs can take a while

var b64DataURIPattern = regexp.MustCompile(`^data:image/(\w+);base64,(.+)$`)

// MinerUReader calls a self-hosted MinerU API to read/convert documents.
type MinerUReader struct {
	endpoint      string
	backend       string // "pipeline", "vlm-*", "hybrid-*"
	vlmServerURL  string // vLLM server URL for vlm-http-client / hybrid-http-client
	formulaEnable bool
	tableEnable   bool
	ocrEnable     bool
	language      string
}

// NewMinerUReader creates a reader from ParserEngineOverrides.
func NewMinerUReader(overrides map[string]string) *MinerUReader {
	c := &MinerUReader{
		endpoint:      strings.TrimRight(overrides["mineru_endpoint"], "/"),
		backend:       stringOr(overrides["mineru_model"], "pipeline"),
		vlmServerURL:  overrides["mineru_vlm_server_url"],
		formulaEnable: parseBoolOr(overrides["mineru_enable_formula"], true),
		tableEnable:   parseBoolOr(overrides["mineru_enable_table"], true),
		ocrEnable:     parseBoolOr(overrides["mineru_enable_ocr"], true),
		language:      stringOr(overrides["mineru_language"], "ch"),
	}
	return c
}

func (c *MinerUReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	if c.endpoint == "" {
		return &types.ReadResult{Error: "MinerU endpoint is not configured"}, nil
	}

	content := req.FileContent
	if len(content) == 0 {
		return &types.ReadResult{Error: "no file content provided"}, nil
	}

	logger.Infof(context.Background(), "[MinerU] Parsing file=%s size=%d via %s", req.FileName, len(content), c.endpoint)

	mdContent, imagesB64, contentList, err := c.callFileParse(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("MinerU file_parse: %w", err)
	}

	// MinerU already returns markdown with embedded HTML blocks (e.g. <table>, <details>).
	// Re-running the whole document through html-to-markdown corrupts valid markdown
	// by escaping headings and image syntax. Only apply narrow compatibility fixes.
	mdContent = normalizeMinerUMarkdown(mdContent)

	// Process images: decode base64, build ImageRef list, replace refs in markdown
	imageRefs, mdContent := c.processImages(mdContent, imagesB64)

	mdContent, imageRefs = ensureOriginalImageRef(req, mdContent, imageRefs)

	pageOffsets := buildMinerUPageOffsets(mdContent, contentList)

	logger.Infof(context.Background(), "[MinerU] Parsed successfully, markdown=%d chars, images=%d, page_offsets=%d", len(mdContent), len(imageRefs), len(pageOffsets))

	return &types.ReadResult{
		MarkdownContent: mdContent,
		ImageRefs:       imageRefs,
		PageOffsets:     pageOffsets,
	}, nil
}

// mineruContentBlock is one entry of MinerU's `content_list`. We only model
// the fields needed to map markdown offsets to source pages.
type mineruContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	TextLevel int    `json:"text_level"`
	ImgPath   string `json:"img_path"`
	ImgCap    any    `json:"img_caption"`
	TableHTML string `json:"table_body"`
	PageIdx   int    `json:"page_idx"`
}

// mineruFileParseResponse mirrors the relevant fields from the MinerU API response.
//
// `content_list` is typed as json.RawMessage because real MinerU deployments
// inconsistently serialize it as either a JSON array (`[{...}]`) or a JSON
// *string* containing an array (`"[{...}]"`). decodeContentList tolerates
// both — and returns nil on anything else so parsing falls back to no page
// tracking instead of failing the whole document.
type mineruFileParseResponse struct {
	Results struct {
		Document struct {
			MDContent   string            `json:"md_content"`
			Images      map[string]string `json:"images"` // path -> "data:image/png;base64,..." or raw base64
			ContentList json.RawMessage   `json:"content_list"`
		} `json:"document"`
		Files struct {
			MDContent   string            `json:"md_content"`
			Images      map[string]string `json:"images"` // path -> "data:image/png;base64,..." or raw base64
			ContentList json.RawMessage   `json:"content_list"`
		} `json:"files"`
	} `json:"results"`
}

// decodeContentList tolerates both JSON-array and JSON-string forms of
// MinerU's content_list. Returns nil silently if the payload is empty or
// neither form parses — page tracking is best-effort and must never block
// document ingestion.
func decodeContentList(raw json.RawMessage) []mineruContentBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var blocks []mineruContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return blocks
	}
	// Try the stringified form: the array was double-encoded as a JSON
	// string literal. Unwrap one layer and retry.
	var inner string
	if err := json.Unmarshal(raw, &inner); err == nil && inner != "" {
		if err := json.Unmarshal([]byte(inner), &blocks); err == nil {
			return blocks
		}
	}
	logger.Warnf(context.Background(), "[MinerU] content_list in unexpected shape (len=%d); skipping page tracking", len(raw))
	return nil
}

func (c *MinerUReader) callFileParse(ctx context.Context, content []byte) (string, map[string]string, []mineruContentBlock, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Form fields
	fields := map[string]string{
		"return_md":           "true",
		"return_images":       "true",
		"table_enable":        fmt.Sprintf("%v", c.tableEnable),
		"formula_enable":      fmt.Sprintf("%v", c.formulaEnable),
		"parse_method":        "ocr",
		"start_page_id":       "0",
		"end_page_id":         "99999",
		"backend":             c.backend,
		"response_format_zip": "false",
		"return_middle_json":  "false",
		"return_model_output": "false",
		"return_content_list": "true",
	}
	if !c.ocrEnable {
		fields["parse_method"] = "txt"
	}
	if c.language != "" {
		fields["lang_list"] = c.language
	}
	if c.vlmServerURL != "" && (strings.HasPrefix(c.backend, "vlm-http-client") || strings.HasPrefix(c.backend, "hybrid-http-client")) {
		fields["server_url"] = c.vlmServerURL
	}
	for k, v := range fields {
		_ = writer.WriteField(k, v)
	}

	// File part
	part, err := writer.CreateFormFile("files", "document")
	if err != nil {
		return "", nil, nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return "", nil, nil, fmt.Errorf("write file content: %w", err)
	}
	writer.Close()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/file_parse", &body)
	if err != nil {
		return "", nil, nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: mineruTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", nil, nil, fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", nil, nil, fmt.Errorf("MinerU API status %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, nil, fmt.Errorf("read response body: %w", err)
	}

	// Dump raw response for debugging (truncate if too large)
	rawStr := string(respBody)
	if len(rawStr) > 4000 {
		logger.Infof(context.Background(), "[MinerU] Raw response (truncated to 4000 chars): %s ...", rawStr[:4000])
	} else {
		logger.Infof(context.Background(), "[MinerU] Raw response: %s", rawStr)
	}

	// Also pretty-print the top-level structure (without large base64 blobs)
	var rawMap map[string]interface{}
	if err := json.Unmarshal(respBody, &rawMap); err == nil {
		c.logMinerUResponseStructure(rawMap, "")
	}

	var result mineruFileParseResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", nil, nil, fmt.Errorf("decode response: %w", err)
	}

	// MinerU response schema differs by version/deployment:
	// - older/self-hosted variants: results.document.*
	// - some variants:            results.files.*
	// Prefer document when available, then fallback to files.
	if result.Results.Document.MDContent != "" || len(result.Results.Document.Images) > 0 {
		logger.Infof(context.Background(), "[MinerU] Using response path: results.document")
		return result.Results.Document.MDContent, result.Results.Document.Images, decodeContentList(result.Results.Document.ContentList), nil
	}
	if result.Results.Files.MDContent != "" || len(result.Results.Files.Images) > 0 {
		logger.Infof(context.Background(), "[MinerU] Using response path: results.files")
		return result.Results.Files.MDContent, result.Results.Files.Images, decodeContentList(result.Results.Files.ContentList), nil
	}

	logger.Errorf(context.Background(), "[MinerU] Response has no markdown/images under results.document or results.files")
	return "", nil, nil, nil
}

// processImages decodes base64 images from MinerU response and returns ImageRef list.
// It also replaces image references in the markdown content.
func (c *MinerUReader) processImages(mdContent string, imagesB64 map[string]string) ([]types.ImageRef, string) {
	var refs []types.ImageRef

	for ipath, b64Str := range imagesB64 {
		matchedRefs := mineruImageOriginalRefs(mdContent, ipath)
		if len(matchedRefs) == 0 {
			continue
		}

		var imgBytes []byte
		var ext string

		if m := b64DataURIPattern.FindStringSubmatch(b64Str); len(m) == 3 {
			ext = m[1]
			decoded, err := base64.StdEncoding.DecodeString(m[2])
			if err != nil {
				logger.Errorf(context.Background(), "[MinerU] Failed to decode base64 image %s: %v", ipath, err)
				continue
			}
			imgBytes = decoded
		} else {
			// raw base64 without data URI prefix
			decoded, err := base64.StdEncoding.DecodeString(b64Str)
			if err != nil {
				logger.Errorf(context.Background(), "[MinerU] Failed to decode raw base64 image %s: %v", ipath, err)
				continue
			}
			imgBytes = decoded
			ext = strings.TrimPrefix(filepath.Ext(ipath), ".")
			if ext == "" {
				ext = "png"
			}
		}

		mimeType := mime.TypeByExtension("." + ext)
		if mimeType == "" {
			mimeType = "image/png"
		}

		for _, originalRef := range matchedRefs {
			refs = append(refs, types.ImageRef{
				Filename:    ipath,
				OriginalRef: originalRef,
				MimeType:    mimeType,
				ImageData:   imgBytes,
			})
		}
	}

	return refs, mdContent
}

// logMinerUResponseStructure logs the structure of the MinerU API response.
func (c *MinerUReader) logMinerUResponseStructure(obj interface{}, prefix string) {
	logResponseStructure("MinerU", obj, prefix)
}

// PingMinerU checks if the self-hosted MinerU service is reachable.
func PingMinerU(endpoint string) (bool, string) {
	endpoint = strings.TrimRight(endpoint, "/")
	if endpoint == "" {
		return false, "未配置 MinerU 端点"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(endpoint + "/docs")
	if err != nil {
		return false, fmt.Sprintf("MinerU 服务不可达: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return false, fmt.Sprintf("MinerU 服务返回状态 %d", resp.StatusCode)
	}
	return true, ""
}

// escapedImageSyntaxPattern matches markdown image references whose `[` was
// over-escaped to `\[` by html-to-markdown. The URL group mirrors the
// downstream image-extraction regex so escapes are only stripped for actual
// image references.
var escapedImageSyntaxPattern = regexp.MustCompile(`!\\\[(.*?)\\?\]\(([^()\n]*(?:\([^)]*\)[^()\n]*)*)\)`)

// escapedHeadingPattern restores markdown headings that were over-escaped to
// \# Heading. We only touch line-leading heading markers to avoid rewriting
// body text that intentionally contains escaped # characters.
var escapedHeadingPattern = regexp.MustCompile(`(?m)^\\(#{1,6})(\s+)`)

// unescapeMarkdownImageSyntax restores `![alt](url)` from html-to-markdown's
// over-escaped `!\[alt\](url)` form. Without this, the downstream image regex
// in ImageResolver fails to match and images are never persisted.
func unescapeMarkdownImageSyntax(content string) string {
	return escapedImageSyntaxPattern.ReplaceAllString(content, "![$1]($2)")
}

func normalizeEscapedMarkdownHeadings(content string) string {
	return escapedHeadingPattern.ReplaceAllString(content, `$1$2`)
}

func normalizeMinerUMarkdown(content string) string {
	content = unescapeMarkdownImageSyntax(content)
	content = normalizeEscapedMarkdownHeadings(content)
	return content
}

func mineruImageOriginalRefs(mdContent, imagePath string) []string {
	normalizedTarget := normalizeMinerUImagePath(imagePath)
	if normalizedTarget == "" {
		return nil
	}

	referenced := extractImageRefsFromContent(mdContent)
	seen := make(map[string]struct{}, len(referenced))
	var matched []string
	for _, refPath := range referenced {
		if normalizeMinerUImagePath(refPath) != normalizedTarget {
			continue
		}
		if _, ok := seen[refPath]; ok {
			continue
		}
		matched = append(matched, refPath)
		seen[refPath] = struct{}{}
	}

	return matched
}

// imgMarkdownPatternAllowSpaces matches markdown image syntax while allowing
// spaces in the URL group, so that paths like "images/第 1 页.jpg" produced by
// MinerU on Chinese documents are still detected as image references.
var imgMarkdownPatternAllowSpaces = regexp.MustCompile(
	`!\[(.*?)\]\(([^()\n]*(?:\([^)]*\)[^()\n]*)*)\)`,
)

func extractImageRefsFromContent(content string) []string {
	var refs []string

	for _, match := range imgMarkdownPatternAllowSpaces.FindAllStringSubmatch(content, -1) {
		if len(match) >= 3 {
			refs = append(refs, strings.TrimSpace(match[2]))
		}
	}
	for _, match := range imgHTMLRelativeSrc.FindAllStringSubmatch(content, -1) {
		if len(match) >= 3 {
			refs = append(refs, match[2])
		}
	}

	return refs
}

// buildMinerUPageOffsets walks MinerU's `content_list` (which lists blocks in
// document order with a `page_idx` per block) and finds where each new page
// begins inside the parsed markdown. Returns one offset per page transition.
//
// We anchor each block by searching forward in mdContent for the first ~80
// characters of its text (or HTML/image path for non-text blocks). Forward-
// only scan handles repeated phrases gracefully and runs in O(N+M).
//
// Blocks that don't anchor (e.g. mineru rewrites the markdown, or text is
// purely whitespace) are skipped without aborting — a missed transition just
// means subsequent chunks may inherit the prior page until the next match,
// which is much better than reporting "p.0" for everything.
func buildMinerUPageOffsets(mdContent string, blocks []mineruContentBlock) []types.PageOffset {
	if mdContent == "" || len(blocks) == 0 {
		return nil
	}

	var offsets []types.PageOffset
	cursor := 0
	currentPage := -1

	for _, blk := range blocks {
		anchor := mineruBlockAnchor(blk)
		if anchor == "" {
			continue
		}

		pos := strings.Index(mdContent[cursor:], anchor)
		if pos < 0 {
			// Anchor not found from cursor — try from start (handles small
			// reorderings without losing later transitions). Skip if still
			// not present anywhere.
			pos = strings.Index(mdContent, anchor)
			if pos < 0 {
				continue
			}
		} else {
			pos += cursor
		}

		page := blk.PageIdx + 1
		if page != currentPage {
			// Record this transition. If we already recorded the same
			// offset earlier (multiple short blocks at file head), keep
			// the later page since blocks scan in order.
			if n := len(offsets); n > 0 && offsets[n-1].Offset == pos {
				offsets[n-1].Page = page
			} else {
				offsets = append(offsets, types.PageOffset{Offset: pos, Page: page})
			}
			currentPage = page
		}
		cursor = pos + len(anchor)
	}

	return offsets
}

// mineruBlockAnchor returns the markdown substring to search for when locating
// this block inside MinerU's md_content. Keeps it short (≤80 chars) so unicode
// boundaries don't break Index() and so the search stays cheap.
func mineruBlockAnchor(blk mineruContentBlock) string {
	switch blk.Type {
	case "text", "equation":
		return trimAnchor(blk.Text)
	case "image":
		return trimAnchor(blk.ImgPath)
	case "table":
		// Table HTML is large; first 80 chars of <table>...<tr>...<td>... is
		// usually unique enough within a single document.
		return trimAnchor(blk.TableHTML)
	default:
		if blk.Text != "" {
			return trimAnchor(blk.Text)
		}
	}
	return ""
}

func trimAnchor(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	const maxLen = 80
	if len(s) <= maxLen {
		return s
	}
	// Cut at the last valid rune boundary within maxLen so unicode is intact.
	cut := maxLen
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}

func normalizeMinerUImagePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if decoded, err := url.PathUnescape(p); err == nil && decoded != "" {
		p = decoded
	}
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimPrefix(p, "images/")
	return p
}
