import {t} from '../i18n'
export function checkField(field:string){
 if(field.endsWith('.readinessProbe'))return t('assessment.readiness')
 if(field.endsWith('.livenessProbe'))return t('assessment.liveness')
 if(field.endsWith('.startupProbe'))return t('assessment.startup')
 if(field.endsWith('.resources.requests.cpu'))return `${t('history.requests')} · CPU`
 if(field.endsWith('.resources.requests.memory'))return `${t('history.requests')} · RAM`
 if(field.endsWith('.resources.limits.cpu'))return `${t('history.limits')} · CPU`
 if(field.endsWith('.resources.limits.memory'))return `${t('history.limits')} · RAM`
 return field
}
export function checkValue(value?:string,code?:string){
 if(value==='missing'||(!value&&(code==='resourceMissing'||code==='cpuPolicy')))return t('assessment.missing')
 if(value==='present')return t('assessment.present')
 return value||t('assessment.unknown')
}
