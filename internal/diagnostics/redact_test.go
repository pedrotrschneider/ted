package diagnostics

import (
	"strings"
	"testing"
)

func TestRedactSensitiveFormsKeepsDiagnosticFields(t *testing.T) {
	for _, s := range []string{
		`password='top sensitive word' code=timeout`,
		`{"password":"quoted \"sensitive\" word","status":503}`,
		"Authorization: Bearer sensitive\nstatus=503",
		"Cookie: first=sensitive; second=also-sensitive\nphase=opening",
		"url=https://private.invalid/sensitive?token=sensitive status=503",
		"refresh_token=ted_r_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopq",
	} {
		v := Redact(s)
		if strings.Contains(v, "sensitive") || strings.Contains(v, "private.invalid") || strings.Contains(v, "ted_r_") {
			t.Fatalf("sensitive content survived redaction: %q", v)
		}
	}
	if got := Redact("phase=buffering session=33333333-3333-4333-8333-333333333333 token_count=3 status=503"); !strings.Contains(got, "token_count=3") || !strings.Contains(got, "status=503") {
		t.Fatal(got)
	}
}
