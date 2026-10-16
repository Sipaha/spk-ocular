import {afterEach, beforeEach, expect, it, vi} from 'vitest'
import {clearDeliveredInput} from './inputResidue'
beforeEach(()=>vi.useFakeTimers())
afterEach(()=>vi.useRealTimers())

it('waits for xterm composition delivery and never clears a new composition',()=>{
 const field=document.createElement('textarea'),sent:string[]=[]
 field.addEventListener('compositionend',()=>setTimeout(()=>sent.push(field.value),0))
 const dispose=clearDeliveredInput(field,()=>false)
 field.dispatchEvent(new CompositionEvent('compositionstart'))
 field.value='日本語';field.dispatchEvent(new CompositionEvent('compositionend'))
 expect(field.value).toBe('日本語');vi.runAllTimers();expect(sent).toEqual(['日本語']);expect(field.value).toBe('')
 field.value='first';field.dispatchEvent(new Event('keyup'))
 field.dispatchEvent(new CompositionEvent('compositionstart'));field.value='次'
 vi.runAllTimers();expect(field.value).toBe('次')
 dispose()
})
it('cancels pending cleanup before a new key or input snapshot, and on disposal',()=>{
 const field=document.createElement('textarea'),dispose=clearDeliveredInput(field,()=>false)
 for(const type of ['keydown','beforeinput']){
  field.value='previous';field.dispatchEvent(new Event('keyup'));field.dispatchEvent(new Event(type));vi.runAllTimers();expect(field.value).toBe('previous')
 }
 field.value='pending';field.dispatchEvent(new Event('keyup'));dispose();vi.runAllTimers();expect(field.value).toBe('pending')
 field.dispatchEvent(new InputEvent('input'));vi.runAllTimers();expect(field.value).toBe('pending')
})
it('retains screen reader announcement text, including a late option change',()=>{
 const field=document.createElement('textarea');let accessible=true
 const dispose=clearDeliveredInput(field,()=>accessible)
 field.value='announced';field.dispatchEvent(new Event('keyup'));vi.runAllTimers();expect(field.value).toBe('announced')
 accessible=false;field.dispatchEvent(new Event('keyup'));accessible=true;vi.runAllTimers();expect(field.value).toBe('announced')
 dispose()
})
