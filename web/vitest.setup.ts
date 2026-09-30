import '@testing-library/jest-dom/vitest'
import { vi } from 'vitest'

// The real @wailsio/runtime installs window listeners and an interval as an
// import-time side effect; jsdom tears `window` down between test files and
// that interval then throws. Nothing here exercises the real bridge.
vi.mock('@wailsio/runtime', () => ({
  Call: { ByName: vi.fn() },
  Events: { On: vi.fn(() => () => {}) },
  Clipboard: { SetText: vi.fn(async () => {}), Text: vi.fn(async () => '') },
  Browser: { OpenURL: vi.fn(async () => {}) },
}))

// RTL drains microtasks with setTimeout(0) and only advances fake timers when
// it detects Jest's; alias a minimal `jest` to Vitest's fake-timer API.
;(globalThis as unknown as { jest?: { advanceTimersByTime(ms: number): void } }).jest = {
  advanceTimersByTime: (ms) => vi.advanceTimersByTime(ms),
}

// jsdom has no layout: scrollIntoView is missing.
if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = vi.fn()

// jsdom measures every element as 0×0, so virtualized lists would render no
// rows. Give elements a screen-sized box.
Element.prototype.getBoundingClientRect = function () {
  return { x: 0, y: 0, top: 0, left: 0, right: 1000, bottom: 800, width: 1000, height: 800, toJSON() {} } as DOMRect
}
Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 800 })
Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, get: () => 1000 })
// CodeMirror measures text through Ranges, which jsdom cannot lay out either.
if (!Range.prototype.getClientRects) Range.prototype.getClientRects = () => [] as unknown as DOMRectList
if (!Range.prototype.getBoundingClientRect) Range.prototype.getBoundingClientRect = () => new DOMRect()

// The dock's tabs are app-wide state: every test starts with none.
import { beforeEach } from 'vitest'
import { useDock } from './src/dock/store'
beforeEach(() => useDock.setState({ tabs: [], active: null, height: 320 }))

// What targets were left looking like (P18) is app-wide too.
import { forgetPages } from './src/components/pageMemo'
beforeEach(() => forgetPages())
