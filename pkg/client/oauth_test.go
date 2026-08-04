package client

import (
	"reflect"
	"testing"
)

// TestBuildScopes_RequestsBothClmScopeNameVariants is a regression test: the CLM
// scope name is unconfirmed between "spring_read"/"spring_write" (the general CLM API
// docs) and "springcm_read"/"springcm_write" (the account-discovery endpoint's own
// docs) — see clmScopes' doc for why both are requested defensively.
func TestBuildScopes_RequestsBothClmScopeNameVariants(t *testing.T) {
	t.Run("includeClm false requests only the base scope", func(t *testing.T) {
		got := buildScopes(false)
		want := []string{"signature"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("buildScopes(false) = %v, want %v", got, want)
		}
	})

	t.Run("includeClm true requests all 4 CLM scope name candidates", func(t *testing.T) {
		got := buildScopes(true)
		want := []string{"signature", "spring_read", "spring_write", "springcm_read", "springcm_write"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("buildScopes(true) = %v, want %v", got, want)
		}
	})
}
