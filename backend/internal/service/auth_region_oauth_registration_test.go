//go:build unit

package service_test

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLoginRegionVerifiedOAuthHasNoDatabaseSideEffects(t *testing.T) {
	for _, address := range []string{"223.5.5.5", "192.168.1.2"} {
		for _, existing := range []bool{false, true} {
			name := address + "/new_account"
			if existing {
				name = address + "/existing_account"
			}
			t.Run(name, func(t *testing.T) {
				assigner := &emailBindDefaultSubAssignerStub{}
				svc, repo, client := newAuthServiceForEmailBindWithRefreshCache(t, map[string]string{
					service.SettingKeyRegistrationEnabled:  "true",
					service.SettingKeyRegionAccessSettings: `{"enabled":true}`,
				}, nil, assigner, newEmailBindRefreshTokenCacheStub())
				ctx := service.WithSessionBinding(context.Background(), &service.SessionBinding{IP: address})
				var existingID int64
				if existing {
					user := &service.User{Email: "region-oauth@example.com", PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive, Concurrency: 1}
					require.NoError(t, repo.Create(ctx, user))
					existingID = user.ID
				}
				identitiesBefore, err := client.AuthIdentity.Query().Count(ctx)
				require.NoError(t, err)
				pair, user, err := svc.LoginOrRegisterVerifiedEmailOAuth(ctx, service.EmailOAuthIdentityInput{
					ProviderType: "github", ProviderSubject: "region-oauth-subject", Email: "region-oauth@example.com", EmailVerified: true, Username: "new-name",
				})
				wantErr := service.ErrMainlandLoginDenied
				if address == "192.168.1.2" {
					wantErr = service.ErrLoginRegionUnavailable
				}
				require.ErrorIs(t, err, wantErr)
				require.Nil(t, pair)
				require.Nil(t, user)
				count, err := client.User.Query().Count(ctx)
				require.NoError(t, err)
				if existing {
					require.Equal(t, 1, count)
					stored, err := client.User.Get(ctx, existingID)
					require.NoError(t, err)
					require.Empty(t, stored.Username)
					require.Nil(t, stored.LastLoginAt)
				} else {
					require.Zero(t, count)
				}
				identities, err := client.AuthIdentity.Query().Count(ctx)
				require.NoError(t, err)
				require.Equal(t, identitiesBefore, identities, "拒绝登录不能创建第三方身份绑定")
				require.Empty(t, assigner.calls)
			})
		}
	}
}
