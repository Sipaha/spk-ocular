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
 return <section className="rbac-workspace flex min-h-0 flex-1 flex-col" aria-label={t('rbac.title')}><div className="relative flex min-h-0 flex-1"><RbacWorkbench scopePicker={scopePicker} client={client} target={target} scope={scope} onResource={open}/>{selected&&<ResourceDrawer {...drawer} subject={selected} onClose={()=>setSelected(null)}/>}</div></section>
}
