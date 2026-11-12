//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type benchmarkGroupFixture struct {
	group   Group
	keys    []APIKey
	created int
}

func (f *benchmarkGroupFixture) GetAvailableGroups(context.Context, int64) ([]Group, error) {
	return []Group{f.group}, nil
}
func (f *benchmarkGroupFixture) GetGroup(context.Context, int64) (*Group, error) {
	return &f.group, nil
}
func (f *benchmarkGroupFixture) GetGroupModelsListCandidates(context.Context, int64, string) ([]string, error) {
	return []string{"other", "allowed", "alias", "allowed"}, nil
}
func (f *benchmarkGroupFixture) List(context.Context, int64, pagination.PaginationParams, APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	return f.keys, nil, nil
}
func (f *benchmarkGroupFixture) Create(_ context.Context, userID int64, input CreateAPIKeyRequest) (*APIKey, error) {
	f.created++
	key := APIKey{UserID: userID, Name: input.Name, GroupID: input.GroupID, Key: "dedicated-secret", Status: StatusActive}
	f.keys = append(f.keys, key)
	return &key, nil
}
func TestModelBenchmarkGroupTargetUsesOwnedGroupKeyAndAllowlist(t *testing.T) {
	ctx := context.Background()
	f := &benchmarkGroupFixture{group: Group{ID: 4, Name: "Premium", Status: StatusActive, Platform: PlatformOpenAI, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"allowed", "alias"}}}}
	s := &ModelBenchmarkGroupService{admin: f, keys: f, baseURL: func(context.Context) (string, error) { return "https://site.example/v1", nil }}
	models, err := s.Models(ctx, 7, 4)
	require.NoError(t, err)
	require.Equal(t, []string{"alias", "allowed"}, models)
	_, err = s.Target(ctx, 7, 4, "other", "medium")
	require.Error(t, err)
	_, err = s.Target(ctx, 7, 5, "allowed", "medium")
	require.Error(t, err)
	require.Zero(t, f.created)
	groupID := int64(4)
	f.keys = []APIKey{{UserID: 999, Name: "model-benchmark-group-4", GroupID: &groupID, Key: "another-users-secret", Status: StatusActive}}
	target, err := s.Target(ctx, 7, 4, "allowed", "medium")
	require.NoError(t, err)
	require.Equal(t, "dedicated-secret", target.APIKey)
	require.Equal(t, "Premium", target.GroupName)
	require.Equal(t, int64(4), target.GroupID)
	_, err = s.Target(ctx, 7, 4, "alias", "high")
	require.NoError(t, err)
	require.Equal(t, 1, f.created, "reuse the same dedicated credential")
	f.keys[1].Status = StatusAPIKeyDisabled
	_, err = s.Target(ctx, 7, 4, "allowed", "medium")
	require.Error(t, err)
	require.Equal(t, 1, f.created, "do not bypass disabled keys by replacing them")
	f.group.Status = "inactive"
	_, err = s.Target(ctx, 7, 4, "allowed", "medium")
	require.Error(t, err)
}

func TestModelBenchmarkGroupURLRequiresTrustedConfiguration(t *testing.T) {
	for _, raw := range []string{"", "/v1", "https://user:secret@site.example", "https://site.example?token=secret", "https://site.example/#fragment", "file:///tmp", "https://site.example?"} {
		_, err := benchmarkGatewayURL(raw)
		require.Error(t, err, raw)
	}
	url, err := benchmarkGatewayURL(" https://site.example/ ")
	require.NoError(t, err)
	require.Equal(t, "https://site.example/v1", url)
}
