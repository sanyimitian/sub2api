package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestLoginRegionPolicy(t *testing.T) {
	ctx := context.Background()
	settings := NewSettingService(newRuntimeSettingRepoStub(), &config.Config{})
	require.NoError(t, settings.CheckLoginRegion(ctx, "223.5.5.5"), "默认关闭")
	require.NoError(t, settings.SetRegionAccessSettings(ctx, &RegionAccessSettings{Enabled: true}))
	for _, address := range []string{"223.5.5.5", "2400:3200::1", "::ffff:223.5.5.5"} {
		require.ErrorIs(t, settings.CheckLoginRegion(ctx, address), ErrMainlandLoginDenied)
	}
	for _, address := range []string{"1.32.192.1", "17.91.136.1", "1.32.199.1", "8.8.8.8", "2001:4860:4860::8888"} {
		require.NoError(t, settings.CheckLoginRegion(ctx, address), address)
	}
	for _, address := range []string{"", "invalid", "192.168.1.2", "::1"} {
		require.ErrorIs(t, settings.CheckLoginRegion(ctx, address), ErrLoginRegionUnavailable)
	}
}

func TestLoginRegionFailedChecksDenyAllRoles(t *testing.T) {
	for _, role := range []string{RoleUser, RoleAdmin} {
		for _, scenario := range []struct {
			name          string
			address       string
			settingsError bool
		}{
			{name: "settings_unavailable", address: "8.8.8.8", settingsError: true},
			{name: "region_unknown", address: "192.168.1.2"},
			{name: "address_missing", address: ""},
		} {
			t.Run(role+"/"+scenario.name, func(t *testing.T) {
				ctx := WithSessionBinding(context.Background(), &SessionBinding{IP: scenario.address})
				repo := newRuntimeSettingRepoStub()
				settings := NewSettingService(repo, &config.Config{})
				if scenario.settingsError {
					repo.getValueFn = func(string) (string, error) { return "", errors.New("offline") }
				} else {
					require.NoError(t, settings.SetRegionAccessSettings(ctx, &RegionAccessSettings{Enabled: true}))
				}
				auth := &AuthService{settingService: settings}
				user := &User{ID: 42, Role: role, Status: StatusActive}
				token, err := auth.GenerateToken(ctx, user)
				require.ErrorIs(t, err, ErrLoginRegionUnavailable)
				require.Empty(t, token)
				pair, err := auth.GenerateTokenPair(ctx, user, "")
				require.ErrorIs(t, err, ErrLoginRegionUnavailable)
				require.Nil(t, pair)
			})
		}
	}
}

func TestLoginRegionNewTokensDeniedButExistingSessionsRefresh(t *testing.T) {
	for _, role := range []string{RoleUser, RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			ctx := WithSessionBinding(context.Background(), &SessionBinding{IP: "223.5.5.5", UserAgent: "test-browser"})
			settings := NewSettingService(newRuntimeSettingRepoStub(), &config.Config{})
			user := &User{ID: 42, Email: "region@example.com", Role: role, Status: StatusActive}
			cache := &regionRefreshCache{tokens: map[string]*RefreshTokenData{}}
			auth := &AuthService{
				cfg:            &config.Config{JWT: config.JWTConfig{Secret: "region-login-test-secret", ExpireHour: 1, RefreshTokenExpireDays: 7}},
				settingService: settings, userRepo: &regionUserRepo{user: user}, refreshTokenCache: cache,
			}
			oldToken, err := auth.GenerateToken(ctx, user)
			require.NoError(t, err)
			pair, err := auth.GenerateTokenPair(ctx, user, "")
			require.NoError(t, err)
			require.NoError(t, settings.SetRegionAccessSettings(ctx, &RegionAccessSettings{Enabled: true}))

			newToken, err := auth.GenerateToken(ctx, user)
			require.ErrorIs(t, err, ErrMainlandLoginDenied)
			require.Empty(t, newToken)
			for _, family := range []string{"", "client-supplied-family-must-not-bypass"} {
				newPair, err := auth.GenerateTokenPair(ctx, user, family)
				require.ErrorIs(t, err, ErrMainlandLoginDenied)
				require.Nil(t, newPair)
			}

			refreshed, err := auth.RefreshToken(ctx, oldToken)
			require.NoError(t, err, "已有旧式会话不受登录限制影响")
			require.NotEmpty(t, refreshed)
			refreshedPair, err := auth.RefreshTokenPair(ctx, pair.RefreshToken)
			require.NoError(t, err, "已有刷新会话不受登录限制影响")
			require.NotEmpty(t, refreshedPair.AccessToken)
			require.NotEqual(t, pair.RefreshToken, refreshedPair.RefreshToken)

			require.NoError(t, settings.SetRegionAccessSettings(ctx, &RegionAccessSettings{Enabled: false}))
			newToken, err = auth.GenerateToken(ctx, user)
			require.NoError(t, err, "关闭后所有角色恢复新登录")
			require.NotEmpty(t, newToken)
			newPair, err := auth.GenerateTokenPair(ctx, user, "")
			require.NoError(t, err)
			require.NotEmpty(t, newPair.AccessToken)
		})
	}
}

type regionUserRepo struct {
	UserRepository
	user *User
}

func (r *regionUserRepo) GetByID(context.Context, int64) (*User, error) { return r.user, nil }

type regionRefreshCache struct {
	RefreshTokenCache
	tokens map[string]*RefreshTokenData
}

func (r *regionRefreshCache) StoreRefreshToken(_ context.Context, key string, value *RefreshTokenData, _ time.Duration) error {
	r.tokens[key] = value
	return nil
}
func (r *regionRefreshCache) GetRefreshToken(_ context.Context, key string) (*RefreshTokenData, error) {
	if value, exists := r.tokens[key]; exists {
		return value, nil
	}
	return nil, ErrRefreshTokenNotFound
}
func (r *regionRefreshCache) ConsumeRefreshToken(ctx context.Context, key string) (*RefreshTokenData, error) {
	value, err := r.GetRefreshToken(ctx, key)
	delete(r.tokens, key)
	return value, err
}
func (r *regionRefreshCache) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}
func (r *regionRefreshCache) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}
