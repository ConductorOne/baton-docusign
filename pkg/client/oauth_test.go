package client

import (
	"reflect"
	"testing"
)

// TestBuildScopes_RequestsImpersonationAndBothClmScopeNameVariants is a regression
// test: "impersonation" is required alongside "spring_read"/"spring_write" (confirmed
// live — see clmScopes' doc), and "springcm_read"/"springcm_write" are still requested
// defensively for the account-discovery endpoint's own docs, which name the scope
// "springcm_read" instead.
func TestBuildScopes_RequestsImpersonationAndBothClmScopeNameVariants(t *testing.T) {
	t.Run("includeClm false requests only the base scope", func(t *testing.T) {
		got := buildScopes(false)
		want := []string{"signature"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("buildScopes(false) = %v, want %v", got, want)
		}
	})

	t.Run("includeClm true requests impersonation and all 4 CLM scope name candidates", func(t *testing.T) {
		got := buildScopes(true)
		want := []string{"signature", "impersonation", "spring_read", "spring_write", "springcm_read", "springcm_write"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("buildScopes(true) = %v, want %v", got, want)
		}
	})
}
