import { expect, it } from 'vitest'
import { logWindowFeatures } from './windowGeometry'

it.each([
  [1920,1040,0,0,'popup,width=1920,height=936,left=0,top=52'],
  [1536,832,-1536,40,'popup,width=1536,height=748,left=-1536,top=82'],
  [1280,680,1920,0,'popup,width=1280,height=612,left=1920,top=34'],
  [320,200,0,0,'popup,width=320,height=180,left=0,top=10'],
])('uses logical work area %s × %s without applying device scale again', (availWidth,availHeight,availLeft,availTop,expected) => {
  expect(logWindowFeatures({availWidth,availHeight,availLeft,availTop})).toBe(expected)
})
it.each([0,-1,NaN,Infinity])('falls back if work area is unavailable (%s)', value => {
  expect(logWindowFeatures({availWidth:value,availHeight:800})).toBe('popup,width=1100,height=750,left=0,top=0')
})
