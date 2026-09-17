package operation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/pflag"
)

// Descriptor is shared by SDK discovery and transport adapters. InputSchema
// deliberately omits defaults: omission and an explicitly supplied value can
// have different business meanings even when their values are equal.
type Descriptor struct {
	Name        string         `json:"name"`
	Path        []string       `json:"path"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func Descriptors(root *Definition) ([]Descriptor, error) {
	if root == nil {
		return nil, fmt.Errorf("operation root is required")
	}
	var result []Descriptor
	seen := make(map[string]string)
	var walk func(*Definition, []string) error
	walk = func(d *Definition, path []string) error {
		if d.Hidden {
			return nil
		}
		if d.Runnable() && len(path) > 0 {
			name := ToolName(path)
			if previous, exists := seen[name]; exists {
				return fmt.Errorf("operations %q and %q share tool name %q", previous, strings.Join(path, " "), name)
			}
			seen[name] = strings.Join(path, " ")
			schema, err := InputSchema(d)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			description := strings.TrimSpace(d.Long)
			if description == "" {
				description = strings.TrimSpace(d.Short)
			}
			result = append(result, Descriptor{Name: name, Path: append([]string(nil), path...), Description: description, InputSchema: schema})
		}
		for _, child := range d.Commands() {
			if err := walk(child, append(append([]string(nil), path...), child.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	for _, child := range root.Commands() {
		if err := walk(child, []string{child.Name()}); err != nil {
			return nil, err
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func ToolName(path []string) string {
	var name strings.Builder
	for _, ch := range strings.ToLower(strings.Join(path, "_")) {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' {
			name.WriteRune(ch)
		} else {
			name.WriteByte('_')
		}
	}
	return "bitwave_" + strings.Trim(name.String(), "_")
}

func InputSchema(d *Definition) (map[string]any, error) {
	properties := make(map[string]any)
	args := map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Ordered positional arguments for " + d.Use}
	var required []string
	if d.Args != nil {
		args["minItems"] = d.Args.Min
		if d.Args.Max >= 0 {
			args["maxItems"] = d.Args.Max
		}
		if d.Args.Min > 0 {
			required = append(required, "arguments")
		}
	}
	properties["arguments"] = args
	var schemaErr error
	d.Flags().VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden {
			return
		}
		if flag.Name == "arguments" {
			schemaErr = fmt.Errorf("flag %q conflicts with reserved positional input", flag.Name)
			return
		}
		kind, item := flagJSONType(flag.Value.Type())
		property := map[string]any{"type": kind, "description": flag.Usage}
		if kind == "array" {
			property["items"] = map[string]any{"type": item}
		}
		properties[flag.Name] = property
		if d.Required(flag.Name) {
			required = append(required, flag.Name)
		}
	})
	if schemaErr != nil {
		return nil, schemaErr
	}
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		sort.Strings(required)
		schema["required"] = required
	}
	return schema, nil
}

func flagJSONType(flagType string) (kind, item string) {
	switch strings.ToLower(flagType) {
	case "bool":
		return "boolean", ""
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "count":
		return "integer", ""
	case "float32", "float64":
		return "number", ""
	case "stringslice", "stringarray":
		return "array", "string"
	case "intslice", "int32slice", "int64slice", "uintslice":
		return "array", "integer"
	case "float32slice", "float64slice":
		return "array", "number"
	case "boolslice":
		return "array", "boolean"
	case "durationslice", "ipslice":
		return "array", "string"
	default:
		// Custom pflag values (including duration, IP, and bytes) retain
		// their documented string representation and Value.Set validation.
		return "string", ""
	}
}
