import { describe, expect, it } from 'vitest'
import { sameLogQuery } from './windowBridge'

describe('log window protocol',()=>{
 it('compares query fields across Wails JSON object key ordering',()=>{
  const owner={channel:'main',previous:false,follow:true,tailLines:500,sinceTime:undefined}
  const guest=JSON.parse('{"channel":"main","follow":true,"previous":false,"tailLines":500}')
  expect(sameLogQuery(owner,guest)).toBe(true)
  expect(sameLogQuery(owner,{...guest,tailLines:100})).toBe(false)
  expect(sameLogQuery(owner,{...guest,sinceTime:'2026-10-09T00:00:00Z'})).toBe(false)
 })
})
