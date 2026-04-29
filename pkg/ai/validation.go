package ai

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

func ValidateToolArguments(tool Tool, call ContentBlock) (map[string]any, error) {
	if strings.TrimSpace(tool.Name) == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	if tool.Parameters == nil {
		return cloneArgumentMap(call.Arguments), nil
	}
	args := cloneArgumentMap(call.Arguments)
	coerced, err := validateAgainstSchema(args, tool.Parameters, "root")
	if err != nil {
		payload, _ := json.MarshalIndent(call.Arguments, "", "  ")
		return nil, fmt.Errorf("validation failed for tool %q: %w\n\nreceived arguments:\n%s", call.Name, err, string(payload))
	}
	asMap, ok := coerced.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("validation failed for tool %q: root must be object", call.Name)
	}
	return asMap, nil
}

func validateAgainstSchema(value any, schema map[string]any, path string) (any, error) {
	if len(schema) == 0 {
		return value, nil
	}

	if allOf, ok := schema["allOf"].([]any); ok {
		var err error
		for _, raw := range allOf {
			sub, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			value, err = validateAgainstSchema(value, sub, path)
			if err != nil {
				return nil, err
			}
		}
	}

	if anyOf, ok := schema["anyOf"].([]any); ok && len(anyOf) > 0 {
		return validateUnionSchema(value, anyOf, path)
	}
	if oneOf, ok := schema["oneOf"].([]any); ok && len(oneOf) > 0 {
		return validateUnionSchema(value, oneOf, path)
	}

	for _, schemaType := range schemaTypes(schema) {
		candidate := coerceByType(value, schemaType)
		validated, err := validateBySingleType(candidate, schema, schemaType, path)
		if err == nil {
			return validated, nil
		}
	}

	if types := schemaTypes(schema); len(types) > 0 {
		return nil, fmt.Errorf("%s must be %s", pathLabel(path), strings.Join(types, " or "))
	}
	return value, nil
}

func validateUnionSchema(value any, options []any, path string) (any, error) {
	var errs []string
	for _, raw := range options {
		sub, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		candidate := deepClone(value)
		validated, err := validateAgainstSchema(candidate, sub, path)
		if err == nil {
			return validated, nil
		}
		errs = append(errs, err.Error())
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("%s failed union validation", pathLabel(path))
	}
	return nil, fmt.Errorf("%s", errs[0])
}

func validateBySingleType(value any, schema map[string]any, schemaType, path string) (any, error) {
	switch schemaType {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s must be object", pathLabel(path))
		}
		return validateObject(object, schema, path)
	case "array":
		array, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("%s must be array", pathLabel(path))
		}
		return validateArray(array, schema, path)
	case "string":
		if _, ok := value.(string); !ok {
			return nil, fmt.Errorf("%s must be string", pathLabel(path))
		}
	case "number":
		if !isNumber(value) {
			return nil, fmt.Errorf("%s must be number", pathLabel(path))
		}
	case "integer":
		if !isInteger(value) {
			return nil, fmt.Errorf("%s must be integer", pathLabel(path))
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return nil, fmt.Errorf("%s must be boolean", pathLabel(path))
		}
	case "null":
		if value != nil {
			return nil, fmt.Errorf("%s must be null", pathLabel(path))
		}
	}
	return value, nil
}

func validateObject(object map[string]any, schema map[string]any, path string) (map[string]any, error) {
	properties, _ := schema["properties"].(map[string]any)
	required := stringSlice(schema["required"])
	for _, key := range required {
		if _, ok := object[key]; !ok {
			return nil, fmt.Errorf("%s is required", childPath(path, key))
		}
	}

	additionalAllowed := true
	if value, ok := schema["additionalProperties"].(bool); ok {
		additionalAllowed = value
	}
	additionalSchema, _ := schema["additionalProperties"].(map[string]any)

	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		value := object[key]
		if propertySchema, ok := properties[key].(map[string]any); ok {
			validated, err := validateAgainstSchema(value, propertySchema, childPath(path, key))
			if err != nil {
				return nil, err
			}
			object[key] = validated
			continue
		}
		if additionalSchema != nil {
			validated, err := validateAgainstSchema(value, additionalSchema, childPath(path, key))
			if err != nil {
				return nil, err
			}
			object[key] = validated
			continue
		}
		if !additionalAllowed {
			return nil, fmt.Errorf("%s is not allowed", childPath(path, key))
		}
	}
	return object, nil
}

func validateArray(array []any, schema map[string]any, path string) ([]any, error) {
	switch items := schema["items"].(type) {
	case map[string]any:
		for i := range array {
			validated, err := validateAgainstSchema(array[i], items, childPath(path, strconv.Itoa(i)))
			if err != nil {
				return nil, err
			}
			array[i] = validated
		}
	case []any:
		for i := range array {
			if i >= len(items) {
				break
			}
			itemSchema, ok := items[i].(map[string]any)
			if !ok {
				continue
			}
			validated, err := validateAgainstSchema(array[i], itemSchema, childPath(path, strconv.Itoa(i)))
			if err != nil {
				return nil, err
			}
			array[i] = validated
		}
	}
	return array, nil
}

func schemaTypes(schema map[string]any) []string {
	switch typed := schema["type"].(type) {
	case string:
		return []string{typed}
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := item.(string); ok {
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func coerceByType(value any, schemaType string) any {
	switch schemaType {
	case "string":
		switch typed := value.(type) {
		case nil:
			return ""
		case bool:
			if typed {
				return "true"
			}
			return "false"
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64)
		case int:
			return strconv.Itoa(typed)
		default:
			return value
		}
	case "number":
		switch typed := value.(type) {
		case nil:
			return float64(0)
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			if err == nil {
				return parsed
			}
		case bool:
			if typed {
				return float64(1)
			}
			return float64(0)
		}
	case "integer":
		switch typed := value.(type) {
		case nil:
			return float64(0)
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			if err == nil && math.Trunc(parsed) == parsed {
				return parsed
			}
		case bool:
			if typed {
				return float64(1)
			}
			return float64(0)
		}
	case "boolean":
		switch typed := value.(type) {
		case nil:
			return false
		case string:
			switch strings.ToLower(strings.TrimSpace(typed)) {
			case "true":
				return true
			case "false":
				return false
			}
		case float64:
			if typed == 1 {
				return true
			}
			if typed == 0 {
				return false
			}
		}
	case "null":
		switch typed := value.(type) {
		case string:
			if typed == "" {
				return nil
			}
		case bool:
			if !typed {
				return nil
			}
		case float64:
			if typed == 0 {
				return nil
			}
		}
	}
	return value
}

func isNumber(value any) bool {
	switch value.(type) {
	case float64, float32, int, int32, int64:
		return true
	default:
		return false
	}
}

func isInteger(value any) bool {
	switch typed := value.(type) {
	case int, int32, int64:
		return true
	case float64:
		return math.Trunc(typed) == typed
	case float32:
		return math.Trunc(float64(typed)) == float64(typed)
	default:
		return false
	}
}

func pathLabel(path string) string {
	if path == "" || path == "root" {
		return "root"
	}
	return path
}

func childPath(path, child string) string {
	if path == "" || path == "root" {
		return child
	}
	return path + "." + child
}

func cloneArgumentMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = deepClone(value)
	}
	return out
}

func deepClone(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = deepClone(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = deepClone(item)
		}
		return out
	default:
		return typed
	}
}
