import '@testing-library/jest-dom/vitest'
import { vi } from 'vitest'

// The real @wailsio/runtime installs window listeners and an interval as an
// import-time side effect; jsdom tears `window` down between test files and
// that interval then throws. Nothing here exercises the real bridge.
vi.mock('@wailsio/runtime', () => ({
  Call: { ByName: vi.fn() },
  Events: { On: vi.fn(() => () => {}) },
}))

// RTL drains microtasks with setTimeout(0) and only advances fake timers when
// it detects Jest's; alias a minimal `jest` to Vitest's fake-timer API.
;(globalThis as unknown as { jest?: { advanceTimersByTime(ms: number): void } }).jest = {
  advanceTimersByTime: (ms) => vi.advanceTimersByTime(ms),
}

// jsdom has no layout: scrollIntoView is missing.
if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = vi.fn()
