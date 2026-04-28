package tools

import (
	"context"
	"fmt"
	"math"
	"reflect"
)

type toolArgValidationError struct {
	tool  string
	cause error
}

func (e *toolArgValidationError) Error() string {
	return fmt.Sprintf("invalid arguments for tool %q: %s", e.tool, e.cause)
}

func (e *toolArgValidationError) Unwrap() error {
	return e.cause
}

func validateToolArgsInContext(ctx context.Context, tool string, schema map[string]interface{}, args map[string]interface{}) error {
	if err := validateToolArgsWithAllowlist(schema, args, InjectedArgKeysFromContext(ctx), true); err != nil {
		return &toolArgValidationError{tool: tool, cause: err}
	}
	return nil
}

// validateToolArgs validates args against a JSON Schema-like map.
// schema is expected to have optional keys: "properties", "required",
// "additionalProperties", "items", and "enum".
func validateToolArgs(schema map[string]interface{}, args map[string]interface{}) error {
	return validateToolArgsWithAllowlist(schema, args, nil, true)
}

func validateToolArgsWithAllowlist(schema map[string]interface{}, args map[string]interface{}, allowedExtras map[string]struct{}, allowExtraAtLevel bool) error {
	if len(schema) == 0 {
		return nil
	}

	if args == nil {
		args = map[string]interface{}{}
	}

	if err := checkRequired(schema, args); err != nil {
		return err
	}

	propsRaw, ok := schema["properties"]
	if !ok {
		return nil
	}

	props, ok := asStringKeyMap(propsRaw)
	if !ok {
		return nil
	}

	additional := allowsAdditional(schema)

	for key, val := range args {
		propSchemaRaw, known := props[key]
		if !known {
			if allowExtraAtLevel {
				if _, ok := allowedExtras[key]; ok {
					continue
				}
			}
			if !additional {
				return fmt.Errorf("unexpected property %q", key)
			}
			continue
		}
		propSchema, ok := asStringKeyMap(propSchemaRaw)
		if !ok {
			continue
		}
		if err := checkType(key, val, propSchema, allowedExtras); err != nil {
			return err
		}
	}

	return nil
}

func checkRequired(schema map[string]interface{}, args map[string]interface{}) error {
	required := extractRequiredFields(schema)
	for _, field := range required {
		if _, present := args[field]; !present {
			return fmt.Errorf("missing required property %q", field)
		}
	}
	return nil
}

func extractRequiredFields(schema map[string]interface{}) []string {
	switch r := schema["required"].(type) {
	case []string:
		return r
	case []interface{}:
		out := make([]string, 0, len(r))
		for _, v := range r {
			s, ok := v.(string)
			if ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func allowsAdditional(schema map[string]interface{}) bool {
	v, ok := schema["additionalProperties"]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func checkType(key string, val interface{}, propSchema map[string]interface{}, allowedExtras map[string]struct{}) error {
	typeRaw, ok := propSchema["type"]
	if !ok {
		return checkEnum(key, val, propSchema)
	}
	typeName, ok := typeRaw.(string)
	if !ok {
		return checkEnum(key, val, propSchema)
	}

	switch typeName {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("property %q: expected string, got %T", key, val)
		}
	case "integer":
		if !isIntegerValue(val) {
			return fmt.Errorf("property %q: expected integer, got %T", key, val)
		}
		if num, ok := toFloat64(val); ok {
			if err := checkNumericBounds(key, num, propSchema); err != nil {
				return err
			}
		}
	case "number":
		if !isNumberValue(val) {
			return fmt.Errorf("property %q: expected number, got %T", key, val)
		}
		if num, ok := toFloat64(val); ok {
			if err := checkNumericBounds(key, num, propSchema); err != nil {
				return err
			}
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("property %q: expected boolean, got %T", key, val)
		}
	case "array":
		arr, ok := asSlice(val)
		if !ok {
			return fmt.Errorf("property %q: expected array, got %T", key, val)
		}
		if err := checkArrayItems(key, arr, propSchema, allowedExtras); err != nil {
			return err
		}
	case "object":
		obj, ok := asStringKeyMap(val)
		if !ok {
			return fmt.Errorf("property %q: expected object, got %T", key, val)
		}
		if err := validateToolArgsWithAllowlist(propSchema, obj, allowedExtras, false); err != nil {
			return fmt.Errorf("property %q: %w", key, err)
		}
	}

	return checkEnum(key, val, propSchema)
}

func checkArrayItems(key string, arr []interface{}, propSchema map[string]interface{}, allowedExtras map[string]struct{}) error {
	itemsRaw, ok := propSchema["items"]
	if !ok {
		return nil
	}
	itemSchema, ok := asStringKeyMap(itemsRaw)
	if !ok {
		return nil
	}
	for i, elem := range arr {
		elemKey := fmt.Sprintf("%s[%d]", key, i)
		if err := checkType(elemKey, elem, itemSchema, allowedExtras); err != nil {
			return err
		}
	}
	return nil
}

func checkEnum(key string, val interface{}, propSchema map[string]interface{}) error {
	enumRaw, ok := propSchema["enum"]
	if !ok {
		return nil
	}

	switch ev := enumRaw.(type) {
	case []interface{}:
		for _, allowed := range ev {
			if reflect.DeepEqual(val, allowed) {
				return nil
			}
		}
	case []string:
		s, ok := val.(string)
		if ok {
			for _, allowed := range ev {
				if s == allowed {
					return nil
				}
			}
		}
	default:
		return nil
	}

	return fmt.Errorf("property %q: value %v is not in enum", key, val)
}

func checkNumericBounds(key string, value float64, propSchema map[string]interface{}) error {
	if minRaw, ok := propSchema["minimum"]; ok {
		min, ok := toFloat64(minRaw)
		if ok && value < min {
			return fmt.Errorf("property %q: value %v is below minimum %v", key, value, min)
		}
	}
	if maxRaw, ok := propSchema["maximum"]; ok {
		max, ok := toFloat64(maxRaw)
		if ok && value > max {
			return fmt.Errorf("property %q: value %v exceeds maximum %v", key, value, max)
		}
	}
	return nil
}

func isIntegerValue(val interface{}) bool {
	switch v := val.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) && float32(math.Trunc(float64(v))) == v
	case float64:
		return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Trunc(v) == v
	default:
		return false
	}
}

func isNumberValue(val interface{}) bool {
	switch v := val.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0)
	case float64:
		return !math.IsNaN(v) && !math.IsInf(v, 0)
	default:
		return false
	}
}

func asSlice(val interface{}) ([]interface{}, bool) {
	if val == nil {
		return nil, false
	}
	if arr, ok := val.([]interface{}); ok {
		return arr, true
	}
	rv := reflect.ValueOf(val)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	out := make([]interface{}, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = rv.Index(i).Interface()
	}
	return out, true
}

func asStringKeyMap(val interface{}) (map[string]interface{}, bool) {
	if val == nil {
		return nil, false
	}
	if m, ok := val.(map[string]interface{}); ok {
		return m, true
	}
	rv := reflect.ValueOf(val)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	out := make(map[string]interface{}, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		out[iter.Key().String()] = iter.Value().Interface()
	}
	return out, true
}

func toFloat64(val interface{}) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	default:
		return 0, false
	}
}
