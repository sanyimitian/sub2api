//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type regionRegistrationEmailCache struct {
	emailCacheStub
	consumed int
}

func (c *regionRegistrationEmailCache) DeleteVerificationCode(context.Context, string) error {
	c.consumed++
	return nil
}

func TestLoginRegionRegistrationHasNoSideEffects(t *testing.T) {
	flows := []struct {
		name string
		run  func(context.Context, *AuthService) (any, *User, error)
	}{
		{"email", func(ctx context.Context, s *AuthService) (any, *User, error) {
			return s.RegisterWithVerification(ctx, "region-signup@example.com", "Password123", "246810", "", "REGION-INVITE", "")
		}},
		{"legacy_oauth", func(ctx context.Context, s *AuthService) (any, *User, error) {
			return s.LoginOrRegisterOAuth(ctx, "region-signup@example.com", "region")
		}},
		{"oauth_token_pair", func(ctx context.Context, s *AuthService) (any, *User, error) {
			return s.LoginOrRegisterOAuthWithTokenPairAndPromoCode(ctx, "region-signup@example.com", "region", "REGION-INVITE", "", "", "oidc")
		}},
		{"oauth_local_email", func(ctx context.Context, s *AuthService) (any, *User, error) {
			return s.RegisterOAuthEmailAccount(ctx, "region-signup@example.com", "Password123", "246810", "REGION-INVITE", "oidc")
		}},
		{"oauth_verified_email", func(ctx context.Context, s *AuthService) (any, *User, error) {
			return s.RegisterVerifiedOAuthEmailAccount(ctx, "region-signup@example.com", "Password123", "REGION-INVITE", "google")
		}},
	}
	for _, scenario := range []struct {
		name, address, settings string
		wantErr                 error
	}{
		{"mainland", "223.5.5.5", `{"enabled":true}`, ErrMainlandLoginDenied},
		{"unknown_region", "192.168.1.2", `{"enabled":true}`, ErrLoginRegionUnavailable},
		{"invalid_settings", "8.8.8.8", `{invalid`, ErrLoginRegionUnavailable},
		{"disabled", "223.5.5.5", `{"enabled":false}`, nil},
		{"overseas", "8.8.8.8", `{"enabled":true}`, nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, flow := range flows {
				t.Run(flow.name, func(t *testing.T) {
					users := &userRepoStub{nextID: 901}
					invitations := &redeemCodeRepoStub{codesByCode: map[string]*RedeemCode{
						"REGION-INVITE": {ID: 7, Code: "REGION-INVITE", Type: RedeemTypeInvitation, Status: StatusUnused},
					}}
					email := &regionRegistrationEmailCache{emailCacheStub: emailCacheStub{data: &VerificationCodeData{
						Code: "246810", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(15 * time.Minute),
					}}}
					quotas := &userPlatformQuotaRepoStub{}
					subscriptions := &defaultSubscriptionAssignerStub{}
					refresh := &regionRefreshCache{tokens: map[string]*RefreshTokenData{}}
					svc := newOAuthEmailFlowAuthService(users, invitations, refresh, map[string]string{
						SettingKeyRegistrationEnabled:                 "true",
						SettingKeyEmailVerifyEnabled:                  "true",
						SettingKeyInvitationCodeEnabled:               "true",
						SettingKeyRegionAccessSettings:                scenario.settings,
						SettingKeyDefaultPlatformQuotas:               `{"openai":{"weekly":12.34}}`,
						SettingKeyDefaultSubscriptions:                `[{"group_id":11,"validity_days":30}]`,
						SettingKeyAuthSourceDefaultEmailGrantOnSignup: "false",
					}, email, quotas)
					svc.defaultSubAssigner = subscriptions
					ctx := WithSessionBinding(context.Background(), &SessionBinding{IP: scenario.address})

					credentials, user, err := flow.run(ctx, svc)
					if scenario.wantErr == nil {
						require.NoError(t, err)
						require.NotEmpty(t, credentials)
						require.NotNil(t, user)
						require.Len(t, users.created, 1)
						if flow.name == "email" {
							require.Equal(t, 1, email.consumed)
							require.Len(t, invitations.useCalls, 1)
							require.Len(t, subscriptions.calls, 1)
							require.Len(t, quotas.bulkInsertCalls, 1)
						}
						return
					}
					require.ErrorIs(t, err, scenario.wantErr)
					require.Empty(t, credentials)
					require.Nil(t, user)
					require.Empty(t, users.created, "地区拒绝必须发生在创建账号之前")
					require.Empty(t, users.updated)
					require.Empty(t, users.deletedIDs, "不依赖事后删除账号补救")
					require.Zero(t, email.consumed, "地区拒绝不能消耗邮箱验证码")
					require.Empty(t, invitations.useCalls)
					require.Equal(t, StatusUnused, invitations.codesByCode["REGION-INVITE"].Status)
					require.Empty(t, subscriptions.calls)
					require.Empty(t, quotas.bulkInsertCalls)
					require.Empty(t, refresh.tokens)
				})
			}
		})
	}
}
