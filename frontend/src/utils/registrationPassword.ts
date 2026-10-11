// 与服务端新用户注册规则一致；已有账号登录不使用此校验。
export function isRegistrationPasswordValid(password: string): boolean {
  return (
    Array.from(password).length >= 6 &&
    /[A-Z]/.test(password) &&
    /[a-z]/.test(password) &&
    /[0-9]/.test(password)
  )
}
