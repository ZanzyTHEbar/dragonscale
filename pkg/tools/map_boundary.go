package tools

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	jsonv2 "github.com/go-json-experiment/json"
)

const (
	defaultMapInlineCutoff = 12
	defaultMapReadLimit    = 100
	maxMapReadLimit        = 500
	maxMapItems            = 5000
)

type mapBoundaryItem struct {
	Index     int
	ItemJSON  string
	InputHash string
}

func parseMapBoundaryItems(args map[string]interface{}) ([]mapBoundaryItem, bool, error) {
	itemsRaw, hasItems := args["items"]
	inputJSONLRaw, hasJSONL := args["input_jsonl"]
	if hasItems == hasJSONL {
		return nil, false, fmt.Errorf("provide exactly one of items or input_jsonl")
	}

	items := make([]mapBoundaryItem, 0)
	if hasItems {
		array, ok := itemsRaw.([]interface{})
		if !ok {
			return nil, false, fmt.Errorf("items must be an array")
		}
		if len(array) == 0 {
			return nil, false, fmt.Errorf("items array is empty")
		}
		if len(array) > maxMapItems {
			return nil, false, fmt.Errorf("items array exceeds max size %d", maxMapItems)
		}
		for idx, item := range array {
			itemJSON, err := canonicalJSONString(item)
			if err != nil {
				return nil, false, fmt.Errorf("serialize item at index %d: %w", idx, err)
			}
			items = append(items, mapBoundaryItem{
				Index:     idx,
				ItemJSON:  itemJSON,
				InputHash: hashString(itemJSON),
			})
		}
		return items, false, nil
	}

	inputJSONL, ok := inputJSONLRaw.(string)
	if !ok || strings.TrimSpace(inputJSONL) == "" {
		return nil, true, fmt.Errorf("input_jsonl must be a non-empty string")
	}
	scanner := bufio.NewScanner(strings.NewReader(inputJSONL))
	// Allow large records while still preventing unbounded memory growth.
	const maxJSONLLineBytes = 4 * 1024 * 1024
	scanner.Buffer(make([]byte, 64*1024), maxJSONLLineBytes)

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item interface{}
		if err := jsonv2.Unmarshal([]byte(line), &item); err != nil {
			return nil, true, fmt.Errorf("invalid JSONL at line %d: %w", lineNo, err)
		}
		itemJSON, err := canonicalJSONString(item)
		if err != nil {
			return nil, true, fmt.Errorf("canonicalize JSONL item at line %d: %w", lineNo, err)
		}
		items = append(items, mapBoundaryItem{
			Index:     len(items),
			ItemJSON:  itemJSON,
			InputHash: hashString(itemJSON),
		})
		if len(items) > maxMapItems {
			return nil, true, fmt.Errorf("input_jsonl exceeds max item count %d", maxMapItems)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, true, fmt.Errorf("read input_jsonl: %w", err)
	}
	if len(items) == 0 {
		return nil, true, fmt.Errorf("input_jsonl contains no records")
	}
	return items, true, nil
}

func resolveMapExecutionMode(args map[string]interface{}, itemCount int, usedJSONL bool) (string, error) {
	if raw, ok := args["execution_mode"]; ok {
		mode, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("execution_mode must be a string")
		}
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "inline":
			return "inline", nil
		case "worker":
			return "worker", nil
		default:
			return "", fmt.Errorf("execution_mode must be one of inline|worker")
		}
	}
	if usedJSONL || itemCount > defaultMapInlineCutoff {
		return "worker", nil
	}
	return "inline", nil
}

func parseMapReadFormat(args map[string]interface{}) (string, error) {
	raw, ok := args["format"]
	if !ok {
		return "json", nil
	}
	format, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("format must be a string")
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json", "":
		return "json", nil
	case "jsonl":
		return "jsonl", nil
	default:
		return "", fmt.Errorf("format must be json or jsonl")
	}
}

func parseMapLimit(args map[string]interface{}, key string, defaultValue, maxValue int) (int, error) {
	value, ok := args[key]
	if !ok {
		return defaultValue, nil
	}
	switch v := value.(type) {
	case float64:
		if v < 0 {
			return 0, fmt.Errorf("%s must be >= 0", key)
		}
		out := int(v)
		if out > maxValue {
			out = maxValue
		}
		return out, nil
	case int:
		if v < 0 {
			return 0, fmt.Errorf("%s must be >= 0", key)
		}
		if v > maxValue {
			return maxValue, nil
		}
		return v, nil
	default:
		return 0, fmt.Errorf("%s must be numeric", key)
	}
}

func parseMapMaxRetries(args map[string]interface{}, defaultValue int) (int, error) {
	raw, ok := args["max_retries"]
	if !ok {
		return defaultValue, nil
	}
	value, err := parseMapLimit(map[string]interface{}{"value": raw}, "value", defaultValue, 10)
	if err != nil {
		return 0, fmt.Errorf("max_retries %w", err)
	}
	return value, nil
}

func parseOptionalStringArg(args map[string]interface{}, key string) (string, error) {
	raw, ok := args[key]
	if !ok {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return strings.TrimSpace(value), nil
}

func canonicalJSONString(v interface{}) (string, error) {
	b, err := jsonv2.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
