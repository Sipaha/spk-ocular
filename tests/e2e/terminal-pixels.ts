import {expect, type Locator, type Page} from '@playwright/test'

/** Read pixels from the actual terminal screenshot, not a second canvas rendering
 * of its text. Buffer/DOM assertions alone cannot detect clipped low glyphs. */
export async function expectUnderscorePixels(page: Page, row: Locator) {
  await expect(row).toContainText('_____')
  const measure = () => row.evaluate(element => {
    const start = element.textContent!.indexOf('_____')
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT)
    const range = document.createRange()
    let node: Node | null, offset = 0
    while ((node = walker.nextNode())) {
      const length = node.textContent!.length
      if (start >= offset && start < offset + length) range.setStart(node, start - offset)
      if (start >= 0 && start + 5 > offset && start + 5 <= offset + length) {
        range.setEnd(node, start + 5 - offset)
        const text = range.getBoundingClientRect(), line = element.getBoundingClientRect()
        return {x: text.x, width: text.width, top: line.top, bottom: line.bottom}
      }
      offset += length
    }
    throw new Error('Underscore text range not found')
  })
  const box = await measure()
  const painted = await page.screenshot({scale: 'css'})
  // Compare with the same scene with text ink hidden. Selection/ANSI
  // backgrounds stay present, and antialiased but visible ink still counts.
  const mask = await page.addStyleTag({content: '.xterm-rows span { color: transparent !important; }'})
  let blank: Buffer
  try { blank = await page.screenshot({scale: 'css'}) }
  finally { await mask.evaluate(element => element.remove()); await mask.dispose() }
  expect(await measure(), 'the sampled row must stay in place across both screenshots').toEqual(box)
  const pixels = await page.evaluate(async ({painted, blank, box}) => {
    const decode = async (png: string) => {
      const image = new Image()
      image.src = 'data:image/png;base64,' + png
      await image.decode()
      const canvas = document.createElement('canvas')
      canvas.width = image.width
      canvas.height = image.height
      const ctx = canvas.getContext('2d')!
      ctx.drawImage(image, 0, 0)
      return ctx
    }
    const [inked, empty] = await Promise.all([decode(painted), decode(blank)])
    // Sample each cell's interior so a neighbouring letter or cursor cannot
    // make five invisible underscores pass the assertion.
    return Array.from({length: 5}, (_, cell) => {
      const left = Math.ceil(box.x + box.width * cell / 5) + 1
      const right = Math.floor(box.x + box.width * (cell + 1) / 5) - 1
      const top = Math.floor(box.top), bottom = Math.ceil(box.bottom)
      const a = inked.getImageData(left, top, right - left, bottom - top).data
      const b = empty.getImageData(left, top, right - left, bottom - top).data
      let ink = 0
      for (let i = 0; i < a.length; i += 4) {
        if (Math.max(Math.abs(a[i] - b[i]), Math.abs(a[i + 1] - b[i + 1]), Math.abs(a[i + 2] - b[i + 2])) > 16) ink++
      }
      return ink
    })
  }, {painted: painted.toString('base64'), blank: blank.toString('base64'), box})
  for (const [cell, ink] of pixels.entries()) expect(ink, `underscore ${cell + 1} must paint inside its row`).toBeGreaterThan(0)
}
