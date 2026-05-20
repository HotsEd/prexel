import { z } from 'zod'
import { MSG } from './messages'

export const loginSchema = z.object({
    email: z
        .string()
        .min(1, MSG.required)
        .email(MSG.email),
    password: z
        .string()
        .min(1, MSG.required)
        .min(6, MSG.minLength(6)),
})

export type LoginValues = z.infer<typeof loginSchema>
