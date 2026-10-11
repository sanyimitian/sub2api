import { describe, expect, it } from 'vitest'
import { isRegistrationPasswordValid } from '../registrationPassword'

describe('registration password policy', () => {
  it.each(['', 'Ab123', '123456789', 'secret123', 'SECRET123', 'SecretPass'])(
    'rejects a password missing a required condition: %s',
    (password) => expect(isRegistrationPasswordValid(password)).toBe(false)
  )
  it.each(['Ab1234', 'Secret-123!', '中文Ab12'])(
    'accepts a password satisfying all conditions: %s',
    (password) => expect(isRegistrationPasswordValid(password)).toBe(true)
  )
})
