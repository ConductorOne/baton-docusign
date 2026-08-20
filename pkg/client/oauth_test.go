package client

import (
	"reflect"
	"testing"
)

// TestBuildScopes_MatchesDocuSignsAuthCodeGrantExample is a regression test: the scope
// list must match DocuSign's own CLM Authentication Overview docs' Authorization Code
// Grant example exactly (signature+spring_read+spring_write) — see clmScopes' doc for
// why "impersonation" (JWT-Grant-only) and "springcm_read"/"springcm_write"
// (unrecognized) were tried and removed.
func TestBuildScopes_MatchesDocuSignsAuthCodeGrantExample(t *testing.T) {
	t.Run("includeClm false requests only the base scope", func(t *testing.T) {
		got := buildScopes(false)
		want := []string{"signature"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("buildScopes(false) = %v, want %v", got, want)
		}
	})

	t.Run("includeClm true requests exactly the documented CLM scopes", func(t *testing.T) {
		got := buildScopes(true)
		want := []string{"signature", "spring_read", "spring_write"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("buildScopes(true) = %v, want %v", got, want)
		}
	})
}
