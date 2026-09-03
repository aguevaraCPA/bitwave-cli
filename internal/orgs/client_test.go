package orgs

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetOrganization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/orgs/org-123" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("missing bearer token")
		}
		_, _ = writer.Write([]byte(`{"org":{"id":"org-123","name":"Acme"}}`))
	}))
	defer server.Close()

	client := New(server.URL, func() (string, error) { return "test-token", nil })
	org, err := client.Get("org-123")
	if err != nil {
		t.Fatal(err)
	}
	if org.ID != "org-123" || org.Name != "Acme" {
		t.Fatalf("unexpected org: %#v", org)
	}
}
