import type { Target } from '../api/types'
import type { Actions } from '../store'
import type { ConfigurationActions } from '../configurations/Configurations'
import type { MenuItem } from '../actions/Menu'
import { t } from '../i18n'

/** Source actions and connection controls reflect the target's actual state. */
export function targetMenuItems(target: Target, act: Actions, configs: ConfigurationActions | null): MenuItem[] {
  const items: MenuItem[] = []
  if(target.connection?.state==='connecting')items.push({id:'cancel',label:t('connection.cancel'),onSelect:()=>void act.cancelConnect(target,target.connection?.id)})
  if(target.provider==='kubernetes'&&configs){
    if(!target.locked)items.push({id:'edit',label:t('configs.edit'),onSelect:()=>configs.openTarget(target,'edit')})
    if(!target.id.startsWith('stored:'))items.push({id:'reveal',label:t('configs.reveal'),onSelect:()=>configs.openTarget(target,'reveal')})
    items.push({id:'rename',label:t('configs.rename'),separator:true,onSelect:()=>configs.openTarget(target,'rename')})
  }
  if(target.connection?.state!=='connecting'&&(target.open||target.connection?.state==='connected'))items.push({id:'close',label:t('target.close'),hint:t('target.closeHint'),separator:items.length>0&&target.provider!=='kubernetes',onSelect:()=>void act.closeTarget(target)})
  if(target.provider==='kubernetes'&&configs)items.push({id:'remove',label:t('configs.remove'),separator:true,danger:true,onSelect:()=>configs.openTarget(target,'remove')})
  return items
}
