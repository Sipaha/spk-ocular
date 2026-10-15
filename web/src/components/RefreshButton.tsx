import type { ComponentProps } from 'react'
import { ActionIconButton } from './ActionIconButton'
import { t } from '../i18n'

/** A shared list-refresh action. Busy covers the operation, not its acceptance. */
export function RefreshButton({ busy = false, label, description, disabled, disableWhileBusy = true, onClick, ...props }: ComponentProps<'button'> & { busy?: boolean; label?: string; description?: string; disableWhileBusy?: boolean }) {
  return <ActionIconButton {...props} label={label ?? t('graph.refresh')} description={description ?? label ?? t('graph.refresh')} disabled={disabled || (busy && disableWhileBusy)} aria-disabled={disabled || (busy && disableWhileBusy)} aria-busy={busy} onClick={event => { if ((!busy || !disableWhileBusy) && !disabled) onClick?.(event) }}>
    <svg aria-hidden="true" viewBox="0 0 24 24" className={'h-4 w-4' + (busy ? ' animate-spin' : '')} fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"><path d="M20 7v5h-5M4 17v-5h5"/><path d="M6.1 7a7 7 0 0 1 11.5-1L20 9M4 15l2.4 3A7 7 0 0 0 18 17"/></svg>
  </ActionIconButton>
}
