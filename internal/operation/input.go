package operation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
)

// BindInput binds structured input to a fresh, invocation-local definition.
// It never parses argv: positional strings are data, and flag values are set
// directly. Fresh definitions retain registered defaults for absent fields.
func BindInput(d *Definition, raw json.RawMessage) ([]string, error) {
	if d == nil {
		return nil, fmt.Errorf("operation definition is required")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var input map[string]any
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("decode operation input: %w", err)
	}
	if input == nil {
		return nil, fmt.Errorf("operation input must be an object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("operation input must contain exactly one JSON object")
	}
	var args []string
	if value, present := input["arguments"]; present {
		values, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("arguments must be an array of strings")
		}
		args = make([]string, len(values))
		for i, value := range values {
			text, ok := value.(string)
			if !ok || strings.ContainsRune(text, 0) {
				return nil, fmt.Errorf("arguments[%d] must be a string without NUL bytes", i)
			}
			args[i] = text
		}
	}
	if err := d.Args.Validate(args); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(input))
	for key := range input {
		if key == "arguments" {
			continue
		}
		flag := d.Flags().Lookup(key)
		if flag == nil || flag.Hidden {
			return nil, fmt.Errorf("unknown input %q", key)
		}
		keys = append(keys, key)
	}
	for required := range d.required {
		if _, present := input[required]; !present {
			return nil, fmt.Errorf("%s is required", required)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		flag := d.Flags().Lookup(key)
		kind, itemKind := flagJSONType(flag.Value.Type())
		if kind == "array" {
			values, ok := input[key].([]any)
			if !ok {
				return nil, fmt.Errorf("%s must be an array", key)
			}
			slice, ok := flag.Value.(pflag.SliceValue)
			if !ok {
				return nil, fmt.Errorf("%s does not support structured arrays", key)
			}
			encoded := make([]string, len(values))
			for index, value := range values {
				text, err := scalarInput(value, itemKind)
				if err != nil {
					return nil, fmt.Errorf("%s[%d]: %w", key, index, err)
				}
				encoded[index] = text
			}
			if err := slice.Replace(encoded); err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			flag.Changed = true
			continue
		}
		text, err := scalarInput(input[key], kind)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if err := d.Flags().Set(key, text); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
	}
	return args, nil
}

func scalarInput(value any, kind string) (string, error) {
	switch kind {
	case "boolean":
		boolean, ok := value.(bool)
		if !ok {
			return "", fmt.Errorf("must be a boolean")
		}
		return strconv.FormatBool(boolean), nil
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return "", fmt.Errorf("must be an integer")
		}
		if index := strings.IndexAny(number.String(), "eE"); index >= 0 {
			exponent, err := strconv.ParseInt(number.String()[index+1:], 10, 32)
			if err != nil || exponent < -400 || exponent > 400 {
				return "", fmt.Errorf("integer exponent is outside supported range")
			}
		}
		// Reject huge exponents before exact rational parsing. Any integer
		// pflag can represent is far inside finite float64 range.
		approximate, err := strconv.ParseFloat(number.String(), 64)
		if err != nil || math.IsInf(approximate, 0) || math.IsNaN(approximate) || len(number.String()) > 1024 {
			return "", fmt.Errorf("integer is outside supported range")
		}
		exact, ok := new(big.Rat).SetString(number.String())
		if !ok || !exact.IsInt() {
			return "", fmt.Errorf("must be an integer")
		}
		return exact.Num().String(), nil
	case "number":
		number, ok := value.(json.Number)
		if !ok {
			return "", fmt.Errorf("must be a number")
		}
		parsed, err := strconv.ParseFloat(number.String(), 64)
		if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
			return "", fmt.Errorf("number is outside supported range")
		}
		return number.String(), nil
	default:
		text, ok := value.(string)
		if !ok || strings.ContainsRune(text, 0) {
			return "", fmt.Errorf("must be a string without NUL bytes")
		}
		return text, nil
	}
}
