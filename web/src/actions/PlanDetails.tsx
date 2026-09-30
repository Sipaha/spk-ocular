import type { ActionPlan } from '../api/types'
import { messageText, t } from '../i18n'
import { ActionLists, Portions } from './ActionLists'

/** What a plan says: why it cannot run, what happens, warnings, changes, the objects concerned and the permission check. */
export function PlanDetails({ plan }: { plan: ActionPlan }) {
  return (
    <>
      {plan.unavailable && (
        <p role="alert" className="rounded-md bg-warning/10 px-3 py-2 text-warning">
          {t('action.unavailable', { reason: messageText(plan.unavailable) })}
        </p>
      )}
      {!!plan.effects?.length && (
        <section aria-label={t('action.effects')}>
          <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">{t('action.effects')}</h3>
          <ul className="list-disc space-y-0.5 pl-5">
            {plan.effects.map((x, i) => (
              <li key={i}>{messageText(x)}</li>
            ))}
          </ul>
        </section>
      )}
      {!!plan.warnings?.length && (
        <section aria-label={t('action.warnings')} className="text-warning">
          <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wider">{t('action.warnings')}</h3>
          <ul className="list-disc space-y-0.5 pl-5">
            {plan.warnings.map((x, i) => (
              <li key={i}>{messageText(x)}</li>
            ))}
          </ul>
        </section>
      )}
      {!!plan.changes?.length && (
        <section aria-label={t('action.changes')}>
          <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">{t('action.changes')}</h3>
          <Portions
            items={plan.changes}
            render={(shown) => (
              <ul className="space-y-0.5 pl-5 font-mono text-xs">
                {shown.map((x, i) => (
                  <li key={i} className="break-all">
                    {messageText(x)}
                  </li>
                ))}
              </ul>
            )}
          />
        </section>
      )}
      {!!plan.lists?.length && <ActionLists lists={plan.lists} />}
      <p aria-label={t('action.rights')} className={plan.rights.state === 'denied' ? 'text-danger' : plan.rights.state === 'unknown' ? 'text-warning' : 'text-fg-muted'}>
        {t('action.rights')}:{' '}
        {plan.rights.state === 'allowed'
          ? t('action.rightsAllowed')
          : plan.rights.state === 'denied'
            ? t('action.rightsDenied', { reason: plan.rights.reason ?? '' })
            : t('action.rightsUnknown') + (plan.rights.reason ? ` (${plan.rights.reason})` : '')}
      </p>
    </>
  )
}
