import { describe, expect, it } from 'vitest'
import { formatAge, formatBytes, formatCPU } from './format'

describe('format', () => {
  it('ages like kubectl', () => {
    expect(formatAge(-5)).toBe('0s')
    expect(formatAge(45_000)).toBe('45s')
    expect(formatAge(12 * 60_000)).toBe('12m')
    expect(formatAge(47 * 3_600_000)).toBe('47h')
    expect(formatAge(5 * 86_400_000)).toBe('5d')
    expect(formatAge(800 * 86_400_000)).toBe('2y')
  })
  it('cpu and bytes', () => {
    expect(formatCPU(0.25)).toBe('250m')
    expect(formatCPU(1.5)).toBe('1.50')
    expect(formatCPU(0.0001)).toBe('<1m')
    expect(formatCPU(0)).toBe('0m')
    expect(formatBytes(512)).toBe('512')
    expect(formatBytes(1536)).toBe('1.5Ki')
    expect(formatBytes(256 * 1024 * 1024)).toBe('256Mi')
  })
})
