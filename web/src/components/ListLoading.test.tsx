import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { CancelledList, ListLoading } from './ListLoading'
import type { ViewState } from '../views/viewSync'
import { setLanguage } from '../i18n'

afterEach(() => {cleanup(); vi.useRealTimers(); setLanguage('en')})
const state = (cancelLoading = vi.fn()): ViewState & {cancelLoading: () => void} => ({viewId: 'v', kind: null, rows: [], resync: true, halted: false, openError: null, loadingStartedAt: Date.now(), status: {state: 'loading', downloadedBytes: 2048}, cancelLoading})
it('shows growing elapsed time and real byte progress with an operable Cancel button', () => {
 vi.useFakeTimers()
 const view=state()
 const {rerender}=render(<ListLoading view={view}/>)
 expect(screen.queryByText(/Received/)).not.toBeInTheDocument()
 expect(screen.getByRole('status',{name:'Loading…'})).toBeVisible()
 expect(screen.queryByRole('button',{name:'Cancel'})).not.toBeInTheDocument()
 expect(screen.queryByText('0 s')).not.toBeInTheDocument()
 act(()=>vi.advanceTimersByTime(2250))
 expect(screen.getByText('2 s · Received 2KiB')).toBeInTheDocument()
 rerender(<ListLoading view={{...view,status:{state:'loading',downloadedBytes:4096}}}/>)
 expect(screen.getByText('2 s · Received 4KiB')).toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'Cancel'}))
 expect(view.cancelLoading).toHaveBeenCalledTimes(1)
})
it('does not fabricate a byte count for providers without telemetry', () => {
 vi.useFakeTimers()
 const view=state();delete view.status.downloadedBytes
 render(<ListLoading view={view}/>)
 expect(screen.queryByText(/Received/)).not.toBeInTheDocument()
 act(()=>vi.advanceTimersByTime(2000))
 expect(screen.getByText('2 s · Received —')).toBeInTheDocument()
})
it('cancelled placeholder has only an explicit retry action and no timer', () => {
 vi.useFakeTimers()
 const retry=vi.fn()
 render(<CancelledList retry={retry}/>)
 expect(screen.getByText('Request cancelled')).toBeInTheDocument()
 expect(vi.getTimerCount()).toBe(0)
 fireEvent.click(screen.getByRole('button',{name:'Retry'}))
 expect(retry).toHaveBeenCalledTimes(1)
})

it('delays inline refresh details for two seconds and resets for a new load', () => {
 vi.useFakeTimers()
 const view=state()
 const {rerender}=render(<ListLoading view={view} inline/>)
 expect(screen.queryByText(/Received/)).not.toBeInTheDocument()
 act(()=>vi.advanceTimersByTime(1750))
 expect(screen.queryByRole('button',{name:'Cancel'})).not.toBeInTheDocument()
 act(()=>vi.advanceTimersByTime(250))
 expect(screen.getByText('2 s · Received 2KiB')).toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'Cancel'}))
 expect(view.cancelLoading).toHaveBeenCalledOnce()
 rerender(<ListLoading view={{...view,loadingStartedAt:Date.now()}} inline/>)
 expect(screen.queryByText(/Received/)).not.toBeInTheDocument()
})
