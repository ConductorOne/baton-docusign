package connector

import (
	"context"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"go.uber.org/zap"
)

// clmRoleBuilder syncs the 5 fixed CLM account-level roles (client.ClmRoles). The role
// set itself isn't backed by an API call — see resource_types.go for why this resource
// type exists — but List() still checks CLM availability via EnsureClmReady before
// emitting it, the same discovery check every other CLM builder's real API call runs
// internally; otherwise these 5 roles would sync unconditionally even on an account
// with no CLM subscription, unlike every other CLM resource type.
type clmRoleBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

func (b *clmRoleBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return clmRoleResourceType
}

// List returns the fixed set of CLM roles, gated on CLM being available for this
// account. No pagination needed — the set is small and hardcoded, not fetched from the
// API — so the availability check always runs (there's no first-page-only gate to
// apply, unlike the paginated CLM builders).
func (b *clmRoleBuilder) List(ctx context.Context, _ *v2.ResourceId, _ rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	if err := b.client.EnsureClmReady(ctx); err != nil {
		// Narrower than every other CLM builder's tolerance (isOptInFeatureUnavailableError):
		// EnsureClmReady is the ONLY CLM call clm_role's List() ever makes, so an error here
		// that ISN'T from CLM account discovery itself can only be eSignature's own
		// ensureInitialized failing (a broken/expired token) — a problem that already breaks
		// every other resource type too, CLM or not, and should fail this sync loudly rather
		// than be silently mistaken for a plain non-CLM account.
		//
		// Residual, accepted risk: the SDK's sync engine runs different resource types'
		// List() concurrently (see vendor's pkg/sync/parallel_syncer.go), so two CLM
		// builders' near-simultaneous discovery calls could in principle still disagree if
		// CLM discovery itself answers inconsistently within one sync — e.g. clm_role sees a
		// discovery failure and skips while clm_folder's later call succeeds and emits
		// grants to clm_role/<name> principals this sync never produced. Not solved with a
		// connector-side retry here: ductone/c1's own connector-error classification
		// (isNonRetryableCode) already treats these exact codes as stable/permanent for a
		// sync's lifetime, specifically because the token doesn't change mid-sync — so
		// retrying would fight that platform convention, and there's no live CLM tenant to
		// validate a bespoke cross-builder consistency mechanism against instead.
		if client.IsClmDiscoveryError(err) {
			clmSkipLogLevel(ctx, err)("baton-docusign: CLM is not available for this account or token, skipping clm_role sync", zap.Error(err))
			return nil, &rs.SyncOpResults{}, nil
		}
		return nil, nil, err
	}

	var resources []*v2.Resource
	for _, role := range client.ClmRoles {
		roleResource, err := rs.NewRoleResource(
			role.Name,
			clmRoleResourceType,
			role.Name,
			nil,
		)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, roleResource)
	}
	return resources, &rs.SyncOpResults{}, nil
}

// Entitlements: clm_role is a pure grant target (like a user/group), not itself an
// entitlement-holder.
func (b *clmRoleBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

// Grants: nothing grants access to a CLM role itself; roles are a principal-like
// grant target for folder security, not a resource with its own membership.
func (b *clmRoleBuilder) Grants(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func newClmRoleBuilder(c *client.Client) *clmRoleBuilder {
	return &clmRoleBuilder{
		resourceType: clmRoleResourceType,
		client:       c,
	}
}
