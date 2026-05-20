import { z } from 'zod'

/** TOTP-style 6-digit codes. */
export const totpCodeSchema = z
    .string()
    .trim()
    .regex(/^\d{6}$/, 'Código deve ter 6 dígitos.')

/** Recovery codes: 8-char alphanumeric, optionally hyphenated (XXXX-XXXX). */
export const recoveryCodeSchema = z
    .string()
    .trim()
    .min(4)
    .max(20)

/** Either TOTP OR recovery. */
export const twoFactorCodeSchema = z.union([totpCodeSchema, recoveryCodeSchema])
