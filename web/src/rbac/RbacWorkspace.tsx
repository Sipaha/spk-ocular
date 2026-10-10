import {useState,type ComponentProps,type ReactNode} from 'react'
import type {Client} from '../api/client'
import type {Ref,ScopeSel} from '../api/types'
import {t} from '../i18n'
import {mayLeave} from '../edit/guard'
import {ResourceDrawer} from '../components/ResourceDrawer'
import RbacWorkbench from './RbacWorkbench'
type Drawer=Omit<ComponentProps<typeof ResourceDrawer>,'subject'|'onClose'>
export default function RbacWorkspace({client,target,scope,scopePicker,drawer}:{client:Client;target:{provider:string;id:string};scope:ScopeSel;scopePicker:ReactNode;drawer:Drawer}){
 const [selected,setSelected]=useState<Ref|null>(null)
 const open=(ref:Ref)=>{if(selected?.uid===ref.uid&&selected?.kind===ref.kind)return;mayLeave(()=>setSelected(ref))}
 return <section className="rbac-workspace flex min-h-0 flex-1 flex-col" aria-label={t('rbac.title')}><header className="flex min-h-10 shrink-0 flex-wrap items-center gap-2 border-b border-line bg-panel px-3 py-1"><h2 className="text-sm font-semibold">{t('rbac.title')}</h2>{scopePicker}</header><div className="relative flex min-h-0 flex-1"><RbacWorkbench client={client} target={target} scope={scope} onResource={open}/>{selected&&<ResourceDrawer {...drawer} subject={selected} onClose={()=>setSelected(null)}/>}</div></section>
}
