//go:build unit

package service

import (
	"context"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestValidateRegistrationPassword(t *testing.T) {
	for _, tc := range []struct {
		name, password string
		valid          bool
	}{
		{"empty", "", false},
		{"too short", "Ab123", false},
		{"digits only", "123456789", false},
		{"missing uppercase", "secret123", false},
		{"missing lowercase", "SECRET123", false},
		{"missing digit", "SecretPass", false},
		{"minimum valid", "Ab1234", true},
		{"symbols allowed", "Secret-123!", true},
		{"unicode allowed", "中文Ab12", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRegistrationPassword(tc.password)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Equal(t, "PASSWORD_TOO_WEAK", infraerrors.Reason(err))
			}
		})
	}
}

func TestRegistrationPathsRejectWeakPasswordBeforeSideEffects(t *testing.T) {
	// 不提供邮箱服务、用户仓库或邀请码仓库：弱密码必须在触碰这些依赖前返回。
	svc := newAuthService(nil, map[string]string{
		SettingKeyRegistrationEnabled:   "true",
		SettingKeyEmailVerifyEnabled:    "true",
		SettingKeyInvitationCodeEnabled: "true",
	}, nil, nil)
	ctx := context.Background()
	for _, password := range []string{"123456", "secret123", "SECRET123", "SecretPass"} {
		t.Run(password, func(t *testing.T) {
			_, user, err := svc.RegisterWithVerification(ctx, "fresh@example.com", password, "123456", "", "INVITE", "")
			require.Nil(t, user)
			require.Equal(t, "PASSWORD_TOO_WEAK", infraerrors.Reason(err))
			_, user, err = svc.RegisterOAuthEmailAccount(ctx, "fresh@example.com", password, "123456", "INVITE", "google")
			require.Nil(t, user)
			require.Equal(t, "PASSWORD_TOO_WEAK", infraerrors.Reason(err))
			_, user, err = svc.RegisterVerifiedOAuthEmailAccount(ctx, "fresh@example.com", password, "INVITE", "google")
			require.Nil(t, user)
			require.Equal(t, "PASSWORD_TOO_WEAK", infraerrors.Reason(err))
		})
	}
}
