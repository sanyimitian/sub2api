package service

import (
	"context"
	"errors"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ipgeo"
)

var (
	ErrMainlandLoginDenied    = infraerrors.Forbidden("MAINLAND_ACCESS_DENIED", "考虑到《生成式人工智能服务管理暂行办法》等相关监管要求以及法规要求，本站暂不向中国大陆地区提供服务。")
	ErrLoginRegionUnavailable = infraerrors.ServiceUnavailable("REGION_LOOKUP_UNAVAILABLE", "暂时无法确认当前网络地址所属地区，请稍后重新检测。")
)

// CheckLoginRegion 只校验新登录，不用于接口密钥调用或已有会话续期。
// 包括管理员在内的所有账号使用相同规则，不依赖身份或角色。
func (s *SettingService) CheckLoginRegion(ctx context.Context, address string) error {
	settings, err := s.GetRegionAccessSettingsCached(ctx)
	if err != nil {
		return ErrLoginRegionUnavailable.WithCause(err)
	}
	if !settings.Enabled {
		return nil
	}
	country, err := ipgeo.Lookup(address)
	if err != nil {
		return ErrLoginRegionUnavailable.WithCause(err)
	}
	if country == "CN" {
		return ErrMainlandLoginDenied
	}
	return nil
}

func (s *AuthService) checkNewLoginRegion(ctx context.Context) error {
	// 未装配设置服务只存在于独立认证测试；生产构造始终提供设置服务。
	if s.settingService == nil {
		return nil
	}
	address := ""
	if binding := SessionBindingFromContext(ctx); binding != nil {
		address = binding.IP
	}
	return s.settingService.CheckLoginRegion(ctx, address)
}

func IsLoginRegionError(err error) bool {
	return errors.Is(err, ErrMainlandLoginDenied) || errors.Is(err, ErrLoginRegionUnavailable)
}
