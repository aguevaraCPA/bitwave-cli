package operation

import (
	"reflect"
	"testing"
)

func TestDescriptorsUseMetadataAndExcludeHidden(t *testing.T) {
	run := func(*Call, []string) error { return nil }
	root := &Definition{Use: "bitwave", RunE: run}
	group := &Definition{Use: "work-space"}
	create := &Definition{Use: "create --name <n> [--base-currency USD]", Short: "Create workspace", Args: NoArgs, RunE: run}
	create.Flags().String("name", "", "Workspace name")
	create.Flags().String("base-currency", "USD", "Base currency")
	create.Flags().Bool("enabled", false, "Enabled")
	create.Flags().Int64("timestamp", 0, "Timestamp")
	create.Flags().Uint64("nonce", 0, "Nonce")
	create.Flags().Float64("ratio", 1.5, "Ratio")
	create.Flags().StringSlice("wallet", nil, "Wallets")
	create.Flags().StringArray("input", nil, "Inputs")
	create.Flags().Duration("timeout", 0, "Timeout")
	create.Flags().String("hidden", "", "")
	_ = create.Flags().MarkHidden("hidden")
	_ = create.MarkFlagRequired("name")
	group.AddCommand(create)
	hidden := &Definition{Use: "secret", Hidden: true}
	hidden.AddCommand(&Definition{Use: "child", RunE: run})
	root.AddCommand(group, hidden)
	descriptors, err := Descriptors(root)
	if err != nil || len(descriptors) != 1 {
		t.Fatalf("descriptors=%#v err=%v", descriptors, err)
	}
	d := descriptors[0]
	if d.Name != "bitwave_work_space_create" || !reflect.DeepEqual(d.Path, []string{"work-space", "create"}) {
		t.Fatalf("descriptor=%#v", d)
	}
	properties := d.InputSchema["properties"].(map[string]any)
	args := properties["arguments"].(map[string]any)
	if args["minItems"] != 0 || args["maxItems"] != 0 {
		t.Fatalf("usage text was mistaken for positional bounds: %#v", args)
	}
	if !reflect.DeepEqual(d.InputSchema["required"], []string{"name"}) {
		t.Fatalf("required=%#v", d.InputSchema["required"])
	}
	for name, want := range map[string]string{
		"name": "string", "base-currency": "string", "enabled": "boolean",
		"timestamp": "integer", "nonce": "integer", "ratio": "number",
		"wallet": "array", "input": "array", "timeout": "string",
	} {
		property := properties[name].(map[string]any)
		if property["type"] != want {
			t.Errorf("%s type=%v want=%s", name, property["type"], want)
		}
		if _, exists := property["default"]; exists {
			t.Errorf("%s schema default would erase presence semantics", name)
		}
	}
	if _, exists := properties["hidden"]; exists {
		t.Fatal("hidden flag exposed")
	}
	if _, err := BindInput(create, encodedInput(t, map[string]any{"name": "My workspace"})); err != nil {
		t.Fatalf("workspace create erroneously requires positional arguments: %v", err)
	}
}

func TestDescriptorsRejectCollisionsAndExposeBounds(t *testing.T) {
	root := &Definition{Use: "bitwave"}
	run := func(*Call, []string) error { return nil }
	root.AddCommand(&Definition{Use: "one-two", RunE: run}, &Definition{Use: "one_two", RunE: run})
	if _, err := Descriptors(root); err == nil {
		t.Fatal("normalized-name collision accepted")
	}
	d := &Definition{Use: "many ID...", Args: MinimumNArgs(1)}
	schema, err := InputSchema(d)
	if err != nil {
		t.Fatal(err)
	}
	args := schema["properties"].(map[string]any)["arguments"].(map[string]any)
	if args["minItems"] != 1 || !reflect.DeepEqual(schema["required"], []string{"arguments"}) {
		t.Fatalf("schema=%#v", schema)
	}
	if _, exists := args["maxItems"]; exists {
		t.Fatal("variadic arguments were bounded")
	}
}
