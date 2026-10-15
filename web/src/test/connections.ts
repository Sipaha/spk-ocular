import { screen,waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { t } from '../i18n'

type User = Pick<ReturnType<typeof userEvent.setup>,'click'>
export async function openConnections(user:User=userEvent){
 const button=await screen.findByRole('button',{name:t('connections.title')})
 if(button.getAttribute('aria-expanded')!=='true')await user.click(button)
}
export async function connectionButton(name:string|RegExp,user:User=userEvent){
 await openConnections(user)
 const label=typeof name==='string'?new RegExp('^'+name.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')+'(?: |$)'):name
 return waitFor(()=>{const button=screen.getAllByRole('button',{name:label}).find(el=>el.classList.contains('connection-choice'));if(!button)throw new Error('connection choice not found');return button})
}
export async function selectConnection(name:string|RegExp,user:User=userEvent){await user.click(await connectionButton(name,user))}
export async function openAppMenu(user:User=userEvent){
 const button=await screen.findByRole('button',{name:t('connections.appMenu')})
 if(button.getAttribute('aria-expanded')!=='true')await user.click(button)
}
