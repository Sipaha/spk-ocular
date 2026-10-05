// Log levels detected from the text.

export type LogLevel = 'ERROR' | 'WARN' | 'INFO' | 'DEBUG' | 'TRACE' | 'UNKNOWN'

export const LOG_LEVELS: LogLevel[] = ['ERROR', 'WARN', 'INFO', 'DEBUG', 'TRACE', 'UNKNOWN']

export const LEVEL_CLASS: Record<LogLevel, string> = {
  ERROR: 'text-log-error',
  WARN: 'text-log-warn',
  INFO: 'text-log-info',
  DEBUG: 'text-log-debug',
  TRACE: 'text-log-trace',
  UNKNOWN: 'text-fg',
}

const LEVEL_PATTERNS: RegExp[] = [
  /\[(ERROR|WARN|WARNING|INFO|DEBUG|TRACE|FATAL)]/i,
  /\|-(ERROR|WARN|INFO|DEBUG|TRACE)\b/i,
  /\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\s+(ERROR|WARN|WARNING|INFO|DEBUG|TRACE|FATAL)\b/i,
  /\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\S*\s+(ERROR|WARN|WARNING|INFO|DEBUG|TRACE|FATAL)\s/i,
  /\blevel=(error|warn|warning|info|debug|trace|fatal)\b/i,
  /"level"\s*:\s*"(error|warn|warning|info|debug|trace|fatal)"/i,
  /^(ERROR|WARN|WARNING|INFO|DEBUG|TRACE|FATAL):/i,
  /\s(ERROR|WARN|INFO|DEBUG|TRACE|FATAL)\s/,
  /^(ERROR|WARN|WARNING|INFO|DEBUG|TRACE|FATAL)[\s:\-[]/i,
  /^[EWIDF]\d{4} \d{2}:\d{2}:\d{2}/, // klog: E0929 12:00:00
]

const KLOG: Record<string, LogLevel> = { E: 'ERROR', W: 'WARN', I: 'INFO', D: 'DEBUG', F: 'ERROR' }

function normalize(s: string): LogLevel {
  const u = s.toUpperCase()
  if (u === 'WARNING') return 'WARN'
  if (u === 'FATAL') return 'ERROR'
  return u as LogLevel
}

export function detectLevel(line: string): LogLevel | null {
  for (const p of LEVEL_PATTERNS) {
    const m = line.match(p)
    if (!m) continue
    if (m[1]) return normalize(m[1])
    return KLOG[line[0]] ?? null
  }
  return null
}
