import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { useRef } from 'react'
import { HelpDialog } from './HelpDialog'
import { useModalFocus } from './modal'

it('keyboard help close is reachable from initial container focus and restores the opener', async () => {
 const opener=document.createElement('button');document.body.append(opener);opener.focus()
 const close=vi.fn();const view=render(<HelpDialog onClose={close}/>);const button=screen.getByRole('button',{name:'Close'})
 await userEvent.tab();expect(button).toHaveFocus();await userEvent.tab({shift:true});expect(button).toHaveFocus()
 await userEvent.keyboard('{Enter}');expect(close).toHaveBeenCalledOnce();view.unmount();expect(opener).toHaveFocus();opener.remove()
})

function Example({close}:{close:()=>void}) {
 const box=useRef<HTMLDivElement>(null);const key=useModalFocus(box,close)
 return <div ref={box} role="dialog" tabIndex={-1} onKeyDown={key}><button disabled>disabled</button><div hidden><button>hidden</button></div><button>first</button><button onKeyDown={event=>{if(event.key==='Escape'){event.preventDefault();event.stopPropagation()}}}>nested</button><a href="#">last</a></div>
}
it('wraps only reachable controls and respects a nested widget Escape',async()=>{
 const close=vi.fn();render(<Example close={close}/>);await userEvent.tab();expect(screen.getByText('first')).toHaveFocus()
 await userEvent.tab({shift:true});expect(screen.getByText('last')).toHaveFocus();await userEvent.tab();expect(screen.getByText('first')).toHaveFocus()
 await userEvent.tab();expect(screen.getByText('nested')).toHaveFocus();await userEvent.keyboard('{Escape}');expect(close).not.toHaveBeenCalled()
 await userEvent.tab();await userEvent.keyboard('{Escape}');expect(close).toHaveBeenCalledOnce()
})
