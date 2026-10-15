import {afterEach,expect,it,vi} from 'vitest'
import {fireEvent} from '@testing-library/react'
import {installTooltips} from './tooltips'
let dispose:(()=>void)|undefined
afterEach(()=>{dispose?.();document.body.replaceChildren();vi.useRealTimers()})
it('replaces native hints, keeps descendant hover stable and dismisses on Escape',()=>{
 vi.useFakeTimers();document.body.innerHTML='<button title="Header hint"><span>Header</span></button>'
 dispose=installTooltips(document)
 const button=document.querySelector('button')!,child=button.firstElementChild!
 fireEvent.mouseOver(button);expect(button).not.toHaveAttribute('title')
 vi.advanceTimersByTime(799);expect(document.querySelector('[role=tooltip]')).toBeNull();vi.advanceTimersByTime(1);expect(document.querySelector('[role=tooltip]')).toHaveTextContent('Header hint');expect(button).toHaveAttribute('aria-describedby','ocular-hover-tooltip')
 fireEvent.mouseOver(child);expect(document.querySelectorAll('[role=tooltip]')).toHaveLength(1)
 fireEvent.keyDown(document,{key:'Escape'});expect(document.querySelector('[role=tooltip]')).toBeNull();expect(button).not.toHaveAttribute('title');expect(button).not.toHaveAttribute('aria-describedby')
 fireEvent.mouseOut(button);expect(button.title).toBe('Header hint')
})
it('measures cells again after resizing and retains hints for icon actions',()=>{
 vi.useFakeTimers();document.body.innerHTML='<span data-cell-tooltip="Full value">Full value</span><section data-connection-picker><button title="Edit appearance">Icon</button></section>'
 dispose=installTooltips(document)
 const cell=document.querySelector('span')!;let width=100,reads=0
 Object.defineProperties(cell,{scrollWidth:{get:()=>{reads++;return 100}},clientWidth:{get:()=>width}})
 fireEvent.mouseOver(cell);expect(reads).toBe(0);vi.advanceTimersByTime(800);expect(reads).toBe(1);expect(document.querySelector('[role=tooltip]')).toBeNull()
 for(let i=0;i<100;i++)fireEvent.resize(window)
 expect(reads).toBe(1)
 width=50;fireEvent.mouseOut(cell);fireEvent.mouseOver(cell);vi.advanceTimersByTime(800);expect(document.querySelector('[role=tooltip]')).toHaveTextContent('Full value')
 fireEvent.mouseOut(cell);fireEvent.mouseOver(document.querySelector('button')!);vi.advanceTimersByTime(800);expect(document.querySelector('[role=tooltip]')).toHaveTextContent('Edit appearance')
})

it('cancels pending hints before any cell measurement when the pointer leaves',()=>{
 vi.useFakeTimers();document.body.innerHTML='<span data-cell-tooltip="value">value</span>'
 dispose=installTooltips(document)
 const cell=document.querySelector('span')!;let reads=0
 Object.defineProperty(cell,'scrollWidth',{get:()=>{reads++;return 100}})
 fireEvent.mouseOver(cell);vi.advanceTimersByTime(100);fireEvent.mouseOut(cell);vi.advanceTimersByTime(800)
 expect(reads).toBe(0);expect(document.querySelector('[role=tooltip]')).toBeNull()
})

it('updates dynamic canvas hints with a fresh delay per node without native titles',()=>{
 vi.useFakeTimers();document.body.innerHTML='<canvas data-tooltip="Service alpha"></canvas>'
 dispose=installTooltips(document);const canvas=document.querySelector('canvas')!
 fireEvent.mouseOver(canvas,{clientX:100,clientY:120});vi.advanceTimersByTime(400)
 fireEvent.mouseMove(canvas,{clientX:110,clientY:125});vi.advanceTimersByTime(399);expect(document.querySelector('[role=tooltip]')).toBeNull()
 vi.advanceTimersByTime(1);expect(document.querySelector('[role=tooltip]')).toHaveTextContent('Service alpha');expect(canvas).not.toHaveAttribute('title')
 fireEvent.pointerDown(canvas,{buttons:1});fireEvent.focusIn(canvas);vi.advanceTimersByTime(800);expect(document.querySelector('[role=tooltip]')).toBeNull();fireEvent.mouseMove(canvas,{clientX:115,clientY:125,buttons:0});expect(document.querySelector('[role=tooltip]')).toBeNull();vi.advanceTimersByTime(800);expect(document.querySelector('[role=tooltip]')).toHaveTextContent('Service alpha')
 canvas.dataset.tooltip='Service beta';fireEvent.mouseMove(canvas,{clientX:200,clientY:250});expect(document.querySelector('[role=tooltip]')).toBeNull()
 vi.advanceTimersByTime(799);expect(document.querySelector('[role=tooltip]')).toBeNull();vi.advanceTimersByTime(1);expect(document.querySelector('[role=tooltip]')).toHaveTextContent('Service beta')
 canvas.dataset.tooltip='';fireEvent.mouseMove(canvas);expect(document.querySelector('[role=tooltip]')).toBeNull()
})
