// Mirrors internal/api (Go) DTOs.

export interface Detail {
  key: string
  value: string
}

export interface Target {
  provider: string
  id: string
  title: string
  subtitle?: string
  current?: boolean
  details?: Detail[]
}

export interface Problem {
  source: string
  message: string
}

export interface TargetGroup {
  provider: string
  title: string
  targets: Target[]
  problems: Problem[]
  error?: string
}

export interface TargetRef {
  provider: string
  id: string
}

export interface TargetsView {
  groups: TargetGroup[]
  selected: TargetRef | null
}

export interface AppInfo {
  name: string
  version: string
  mode: 'desktop' | 'browser'
  language: 'ru' | 'en'
}

export type EventType = 'targets_changed'

export interface ApiEvent {
  type: EventType
  payload?: Record<string, unknown>
}
