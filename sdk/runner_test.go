package sdk

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestNormalizeArgsDefaultsToHelp(t *testing.T) {
	got := NormalizeArgs(nil)
	if len(got) != 1 || got[0] != "--help" {
		t.Fatalf("NormalizeArgs(nil) = %q, want [--help]", got)
	}
}

func TestExecuteDoesNotChangeInvocationEnvironment(t *testing.T) {
	t.Setenv("BITWAVE_ORG_ID", "prior-org")
	result := ExecuteWithOptions(context.Background(), ExecuteOptions{
		Args:             []string{"report", "balance", "--help"},
		WorkingDirectory: t.TempDir(),
		OrganizationID:   "org-from-session",
	})
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, "Balance Report") {
		t.Fatalf("result = %#v", result)
	}
	if got := os.Getenv("BITWAVE_ORG_ID"); got != "prior-org" {
		t.Fatalf("BITWAVE_ORG_ID = %q, want prior-org", got)
	}
}

func TestExecuteDefaultsToRootHelp(t *testing.T) {
	result := Execute(context.Background(), nil, "")
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, "Bitwave accounting and platform operations") || !strings.Contains(result.Stdout, "Usage:") {
		t.Fatalf("result = %#v", result)
	}
}

func TestValidateArgsRejectsNULBytes(t *testing.T) {
	if err := ValidateArgs([]string{"report", "balance\x00unsafe"}); err == nil {
		t.Fatal("expected NUL byte to be rejected")
	}
}
