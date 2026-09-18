package operation

import (
	"reflect"
	"testing"
)

func TestEndpointDeclarationsValidateAndCopyMetadata(t *testing.T) {
	d := &Definition{Use: "future", RunE: func(*Call, []string) error { return nil }}
	d.Flags().String("url", "", "Destination")
	d.Flags().String("another", "", "Other destination")
	d.Flags().Bool("raw-url", false, "Return a URL rather than its content")
	for _, name := range []string{"missing", "raw-url"} {
		if err := d.MarkEndpointParameter(name); err == nil {
			t.Fatalf("accepted invalid endpoint definition %q", name)
		}
	}
	for _, name := range []string{"url", "another"} {
		if err := d.MarkEndpointParameter(name); err != nil {
			t.Fatal(err)
		}
	}
	got := d.EndpointParameters()
	if !reflect.DeepEqual(got, []string{"another", "url"}) {
		t.Fatalf("unsorted endpoint metadata: %v", got)
	}
	got[0] = "mutated"
	root := &Definition{Use: "bitwave"}
	root.AddCommand(d)
	list, err := Descriptors(root)
	if err != nil || len(list) != 1 || !reflect.DeepEqual(list[0].EndpointParameters, []string{"another", "url"}) {
		t.Fatalf("descriptor metadata: %+v %v", list, err)
	}
	if err := d.ValidateEndpointOverrides(false); err != nil {
		t.Fatalf("omitted endpoint rejected: %v", err)
	}
	if err := d.Flags().Set("raw-url", "true"); err != nil {
		t.Fatal(err)
	}
	if err := d.ValidateEndpointOverrides(false); err != nil {
		t.Fatalf("ordinary URL-related data confused with destination: %v", err)
	}
}
