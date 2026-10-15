import {expect,it} from 'vitest'
import {installFocusMode} from './focusMode'
it('shows focus rings only after Tab and cleans up its document listeners',()=>{
 const remove=installFocusMode(document),root=document.documentElement
 const key=(key:string,init:KeyboardEventInit={})=>document.dispatchEvent(new KeyboardEvent('keydown',{key,...init}))
 key('ArrowDown');expect(root).not.toHaveAttribute('data-tab-focus')
 key('Tab',{ctrlKey:true});expect(root).not.toHaveAttribute('data-tab-focus')
 key('Tab',{shiftKey:true});expect(root).toHaveAttribute('data-tab-focus','true')
 document.dispatchEvent(new Event('pointerdown'));expect(root).not.toHaveAttribute('data-tab-focus')
 key('Tab');expect(root).toHaveAttribute('data-tab-focus','true')
 remove();expect(root).not.toHaveAttribute('data-tab-focus');key('Tab');expect(root).not.toHaveAttribute('data-tab-focus')
})
