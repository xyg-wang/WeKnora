package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strconv"
)

// JSON is a custom type that wraps json.RawMessage.
// Used for storing JSON data in the database.
type JSON json.RawMessage

// Scan implements the sql.Scanner interface.
func (j *JSON) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("type assertion to []byte or string failed")
	}

	if len(bytes) == 0 {
		*j = nil
		return nil
	}

	result := json.RawMessage{}
	err := json.Unmarshal(bytes, &result)
	*j = JSON(result)
	return err
}

// Value implements the driver.Valuer interface.
func (j JSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	return json.RawMessage(j).MarshalJSON()
}

// MarshalJSON implements the json.Marshaler interface.
func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}

// UnmarshalJSON implements the json.Unmarshaler interface.
func (j *JSON) UnmarshalJSON(data []byte) error {
	if j == nil {
		return errors.New("JSON: UnmarshalJSON on nil pointer")
	}
	copied := make([]byte, len(data))
	copy(copied, data)
	*j = JSON(copied)
	return nil
}

// ToString converts JSON to a string.
func (j JSON) ToString() string {
	if len(j) == 0 {
		return "{}"
	}
	return string(j)
}

// Map converts JSON to a map.
func (j JSON) Map() (map[string]interface{}, error) {
	if len(j) == 0 {
		return map[string]interface{}{}, nil
	}

	var m map[string]interface{}
	err := json.Unmarshal(j, &m)
	return m, err
}

// PageMetadataFromChunkMetadata extracts the scalar starting page and the
// full covered page list stored in Chunk.Metadata. It accepts both the legacy
// string form ("page_no": "6") and numeric JSON values.
func PageMetadataFromChunkMetadata(meta JSON) (int, []int) {
	if len(meta) == 0 {
		return 0, nil
	}
	var m map[string]any
	if err := json.Unmarshal(meta, &m); err != nil {
		return 0, nil
	}
	pageNo := intFromMetadataValue(m["page_no"])
	pageNos := intsFromMetadataValue(m["page_nos"])
	if len(pageNos) == 0 && pageNo > 0 {
		pageNos = []int{pageNo}
	}
	if pageNo == 0 && len(pageNos) > 0 {
		pageNo = pageNos[0]
	}
	return pageNo, pageNos
}

func intFromMetadataValue(raw any) int {
	switch v := raw.(type) {
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return 0
}

func intsFromMetadataValue(raw any) []int {
	values, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(values))
	seen := make(map[int]struct{}, len(values))
	for _, v := range values {
		n := intFromMetadataValue(v)
		if n <= 0 {
			continue
		}
		if _, exists := seen[n]; exists {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}
