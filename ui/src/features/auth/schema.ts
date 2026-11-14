import { z } from 'zod'

// The admin token is 64 hex characters; API tokens (phase 1) start with dsm_.
export const ADMIN_TOKEN = /^[0-9a-fA-F]{64}$/
export const API_TOKEN = /^dsm_[A-Za-z0-9_-]{8,}$/

export const loginSchema = z.object({
  username: z.string(),
  token: z
    .string()
    .trim()
    .min(1, 'Enter your token.')
    .refine((value) => ADMIN_TOKEN.test(value) || API_TOKEN.test(value), {
      message: 'Paste the 64-character admin token, or an API token that starts with dsm_.',
    }),
})

export type LoginValues = z.infer<typeof loginSchema>
