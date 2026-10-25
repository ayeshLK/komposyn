package auth

import (
	"strings"
	"testing"
)

func TestEnvironmentResolverNeverRevealsMissingValue(t *testing.T) {
	r := EnvironmentResolver{LookupEnv: func(k string) (string, bool) {
		if k == "API_KEY" {
			return "secret", true
		}
		return "", false
	}}
	v, e := r.Resolve("env:API_KEY")
	if e != nil || v != "secret" {
		t.Fatalf("v=%q e=%v", v, e)
	}
	_, e = r.Resolve("env:MISSING")
	if e == nil || strings.Contains(e.Error(), "secret") {
		t.Fatalf("missing credential error=%v", e)
	}
	if e = ValidateReference("env:BAD-NAME"); e == nil {
		t.Fatal("invalid reference accepted")
	}
}
