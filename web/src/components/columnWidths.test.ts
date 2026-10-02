import { expect, it } from 'vitest'
import { columnWidthsKey, parseColumnWidths } from './columnWidths'

it('restores widths by kind and column identity and discards corrupt entries', () => {
  expect(parseColumnWidths({
    [columnWidthsKey('pods')]: '{"name":240,"cpu":80,"bad":-1,"large":9000,"text":"100"}',
    [columnWidthsKey('apps/deployments')]: '{"name":360}',
    'columnWidths.broken': 'not json', 'columnWidths.array': '[100]', kind: '"pods"',
  })).toEqual({ pods: { name: 240, cpu: 80 }, 'apps/deployments': { name: 360 } })
})
