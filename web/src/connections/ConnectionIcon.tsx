import type { CSSProperties } from 'react'
import type { Client } from '../api/client'
import type { Target } from '../api/types'
import { ProviderIcon } from '../components/icons'
import { iconPaths, palette, useAppearance, type Appearance } from './appearance'

export function AppearanceIcon({value,className='',tone='dark'}:{value:Appearance;className?:string;tone?:'light'|'dark'}){
 const colors=palette[value.palette],i=tone==='light'?0:2
 return <span aria-hidden="true" data-connection-icon={value.icon} data-connection-palette={value.palette} className={`connection-avatar ${className}`} style={{color:colors[i],backgroundColor:colors[i+1]} as CSSProperties}>
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.65" strokeLinecap="round" strokeLinejoin="round"><path d={iconPaths[value.icon]}/></svg>
 </span>
}
export function ConnectionIcon({client,target,className=''}:{client:Client;target:Target;className?:string}){
 const value=useAppearance(client,target)
 return value?<AppearanceIcon value={value} className={className}/>:<span aria-hidden="true" className={`connection-avatar bg-hover text-fg-muted ${className}`}><ProviderIcon provider={target.provider} className="h-4 w-4"/></span>
}
