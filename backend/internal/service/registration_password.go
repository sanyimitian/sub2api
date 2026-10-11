package service

import (
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ValidateRegistrationPassword 只校验新用户自行设置的密码，兼容已有账号登录和自动生成的 OAuth 密码。
func ValidateRegistrationPassword(password string) error {
	var upper, lower, digit bool
	for _, char := range password {
		upper = upper || (char >= 'A' && char <= 'Z')
		lower = lower || (char >= 'a' && char <= 'z')
		digit = digit || (char >= '0' && char <= '9')
	}
	if utf8.RuneCountInString(password) < 6 || !upper || !lower || !digit {
		return infraerrors.BadRequest("PASSWORD_TOO_WEAK", "密码至少需要 6 个字符，并同时包含大写字母、小写字母和数字")
	}
	return nil
}
