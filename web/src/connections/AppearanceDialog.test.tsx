import {act,cleanup,render,screen,waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {afterEach,expect,it,vi} from 'vitest'
import {setLanguage} from '../i18n'
import {fakeClient} from '../test/fakeClient'
import {AppearanceDialog} from './AppearanceDialog'

afterEach(cleanup)
it('waits for stored appearance rather than saving a placeholder during a slow read',async()=>{
 setLanguage('en');const {client}=fakeClient([]),close=vi.fn()
 let resolve!:(value:Record<string,string>)=>void
 vi.mocked(client.getTargetState).mockImplementation(()=>new Promise(r=>{resolve=r}))
 render(<AppearanceDialog client={client} target={{provider:'kubernetes',id:'stable',title:'Slow profile'}} onClose={close}/>)
 expect(screen.getByRole('status')).toBeInTheDocument()
 expect(screen.getByRole('button',{name:'Save'})).toBeDisabled()
 expect(screen.queryByRole('button',{name:/^Icon 1$/})).not.toBeInTheDocument()
 await act(async()=>resolve({appearance:JSON.stringify({icon:'cat',palette:'amber'})}))
 expect(await screen.findByRole('button',{name:/^Icon 32$/})).toHaveAttribute('aria-pressed','true')
 expect(screen.getByRole('button',{name:/^Color 4$/})).toHaveAttribute('aria-pressed','true')
 await userEvent.click(screen.getByRole('button',{name:'Save'}))
 await waitFor(()=>expect(close).toHaveBeenCalled())
 expect(client.setTargetState).toHaveBeenCalledExactlyOnceWith('kubernetes','stable','appearance',JSON.stringify({icon:'cat',palette:'amber'}))
})
