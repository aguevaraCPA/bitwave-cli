package operation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func encodedInput(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestBindInputPreservesIntegersBooleansAndPositionalData(t *testing.T) {
	d := &Definition{Use: "add ID", Args: ExactArgs(1)}
	enabled := d.Flags().Bool("enabled", false, "")
	unset := d.Flags().Bool("unset", false, "")
	stamp := d.Flags().Int64("sync-start-sec", 0, "")
	large := d.Flags().Uint64("gas-limit", 0, "")
	args, err := BindInput(d, encodedInput(t, map[string]any{
		"arguments": []string{"--token=positional-data"}, "enabled": false,
		"sync-start-sec": json.Number("1.7040672e9"),
		"gas-limit":      json.Number("18446744073709551615"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if *enabled || !d.Flags().Changed("enabled") || *unset || d.Flags().Changed("unset") {
		t.Fatal("explicit false and omission were not preserved")
	}
	if *stamp != 1704067200 || *large != ^uint64(0) {
		t.Fatalf("integers changed: stamp=%d large=%d", *stamp, *large)
	}
	if !reflect.DeepEqual(args, []string{"--token=positional-data"}) {
		t.Fatalf("positional data was interpreted: %q", args)
	}
}

func TestBindInputReplacesSlicesWithoutCSVInterpretation(t *testing.T) {
	d := &Definition{Use: "filter", Args: NoArgs}
	names := d.Flags().StringSlice("names", []string{"default"}, "")
	postings := d.Flags().StringArray("posting", []string{"default"}, "")
	empty := d.Flags().StringSlice("empty", []string{"default"}, "")
	integers := d.Flags().Int64Slice("ints", nil, "")
	floats := d.Flags().Float64Slice("floats", nil, "")
	booleans := d.Flags().BoolSlice("bools", nil, "")
	durations := d.Flags().DurationSlice("delays", nil, "")
	_, err := BindInput(d, encodedInput(t, map[string]any{
		"names": []string{"Wallet, Inc.", " spaced "}, "posting": []string{"Expenses:Food 1,000 USD"},
		"empty": []string{}, "ints": []json.Number{"9007199254740993"},
		"floats": []float64{1.25}, "bools": []bool{false, true}, "delays": []string{"2s"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*names, []string{"Wallet, Inc.", " spaced "}) ||
		!reflect.DeepEqual(*postings, []string{"Expenses:Food 1,000 USD"}) ||
		len(*empty) != 0 || (*integers)[0] != 9007199254740993 ||
		(*floats)[0] != 1.25 || !reflect.DeepEqual(*booleans, []bool{false, true}) ||
		(*durations)[0] != 2*time.Second {
		t.Fatal("structured slice values were changed")
	}
	for _, name := range []string{"names", "posting", "empty", "ints", "floats", "bools", "delays"} {
		if !d.Flags().Changed(name) {
			t.Fatalf("%s was not marked supplied", name)
		}
	}
}

func TestBindInputDefaultsAndScalarTypes(t *testing.T) {
	d := &Definition{Use: "values"}
	text := d.Flags().String("text", "default", "")
	timeout := d.Flags().Duration("timeout", time.Second, "")
	ratio := d.Flags().Float64("ratio", 1, "")
	small := d.Flags().Int8("small", 1, "")
	unsigned := d.Flags().Uint32("unsigned", 1, "")
	_, err := BindInput(d, encodedInput(t, map[string]any{
		"timeout": "250ms", "ratio": 1.5, "small": -128, "unsigned": 4294967295,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if *text != "default" || d.Flags().Changed("text") || *timeout != 250*time.Millisecond ||
		*ratio != 1.5 || *small != -128 || *unsigned != 4294967295 {
		t.Fatal("scalar binding or registered defaults changed")
	}
}

func TestBindInputRejectsInvalidInput(t *testing.T) {
	cases := []any{
		map[string]any{"unknown": true}, map[string]any{"enabled": "false"},
		map[string]any{"count": 1.25}, map[string]any{"count": "123"},
		map[string]any{"count": json.Number("1e-999999999")},
		map[string]any{"count": json.Number("999999999999999999999")},
		map[string]any{"unsigned": -1}, map[string]any{"text": false},
		map[string]any{"text": "bad\x00value"}, map[string]any{"text": nil},
		map[string]any{"names": "one"}, map[string]any{"names": []any{1}},
		map[string]any{"arguments": "id"}, map[string]any{"arguments": []any{1}},
		map[string]any{"arguments": []string{"bad\x00arg"}}, map[string]any{"hidden": "x"},
		map[string]any{"delay": "nonsense"}, map[string]any{"stdin": "not-a-flag"},
	}
	for _, value := range cases {
		t.Run(string(encodedInput(t, value)), func(t *testing.T) {
			d := &Definition{Use: "test"}
			d.Flags().Bool("enabled", false, "")
			d.Flags().Int64("count", 0, "")
			d.Flags().Uint64("unsigned", 0, "")
			d.Flags().String("text", "", "")
			d.Flags().StringSlice("names", nil, "")
			d.Flags().Duration("delay", 0, "")
			d.Flags().String("hidden", "", "")
			_ = d.Flags().MarkHidden("hidden")
			if _, err := BindInput(d, encodedInput(t, value)); err == nil {
				t.Fatalf("invalid input accepted: %#v", value)
			}
		})
	}
	for _, raw := range []string{"null", "[]", "1", "{} {}", "{", "{\"count\":NaN}"} {
		if _, err := BindInput(&Definition{Use: "test"}, json.RawMessage(raw)); err == nil {
			t.Errorf("invalid JSON object accepted: %s", raw)
		}
	}
}

func TestBindInputBoundsAndRequiredFlags(t *testing.T) {
	d := &Definition{Use: "update ID", Args: ExactArgs(1)}
	d.Flags().String("name", "", "")
	if err := d.MarkFlagRequired("name"); err != nil {
		t.Fatal(err)
	}
	if _, err := BindInput(d, encodedInput(t, map[string]any{"arguments": []string{"id"}})); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("missing required flag: %v", err)
	}
	if _, err := BindInput(d, encodedInput(t, map[string]any{"name": "x"})); err == nil {
		t.Fatal("missing positional argument accepted")
	}
	if _, err := BindInput(d, encodedInput(t, map[string]any{"name": "x", "arguments": []string{"id", "extra"}})); err == nil {
		t.Fatal("extra positional argument accepted")
	}
}
