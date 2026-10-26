import {t, type MessageKey} from '../i18n'

/** Explain the useful output before the acquisition and interpretation limits. */
export function AssessmentIntro({purpose,notice,extra}:{purpose:MessageKey;notice:MessageKey;extra?:MessageKey}) {
 return <div className="space-y-2 text-sm leading-relaxed">
  <p>{t(purpose)}</p>
  <details className="text-xs text-fg-muted">
   <summary className="cursor-pointer focus-visible:outline-2 focus-visible:outline-accent">{t('assessment.limits')}</summary>
   <p className="mt-2">{t(notice)}</p>{extra&&<p className="mt-2">{t(extra)}</p>}
  </details>
 </div>
}
