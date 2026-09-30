import { ApiError } from './api/client'
import { messageText } from './i18n'

/**
 * What a rejection says, as text only (callers add the class where they
 * show one): an API error's reason in the UI's language, else its detail,
 * else its code; any other rejection its message or its text.
 */
export function errorDetail(e: unknown): string {
  if (e instanceof ApiError) return e.why ? messageText(e.why) : e.detail || e.code
  return e instanceof Error ? e.message : String(e)
}
