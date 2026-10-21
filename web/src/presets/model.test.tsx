import {render,screen,within,fireEvent} from '@testing-library/react'
import {describe,it,expect,vi} from 'vitest'
import type {Column,Row,ViewPreset} from '../api/types'
import {ResourceTable,matchesRow} from '../components/ResourceTable'
import {columnsFor,rowsFor,labelID,labelState,validLabelKeys} from './model'
const base:Column[]=[{id:'name',title:'Name',type:'text'},{id:'age',title:'Age',type:'age'}]
const preset:ViewPreset={name:'Team',labels:['x:y/z.a','missing'],hidden:['age'],order:[labelID('x:y/z.a'),'name'],filter:'',sort:{column:labelID('x:y/z.a')}}
const row=(id:string,value:string):Row=>({id,ref:{provider:'kubernetes',target:'demo',kind:'pods',name:id},cells:[{text:id},{time:10}],health:{state:'ok'},labels:{'x:y/z.a':{state:'value',value},missing:{state:'missing'}}})
describe('bounded exact metadata label columns',()=>{
 it('keeps literal keys and original cells aligned through order/hide and immutable projection',()=>{
  const input=[row('a','v2')],cols=columnsFor(base,preset),rows=rowsFor(input,base,cols,preset)
  expect(cols.map(c=>c.id)).toEqual([labelID('x:y/z.a'),'name','age',labelID('missing')]);expect(cols[2].hidden).toBe(true)
  expect(rows[0].cells.map(c=>c.text)).toEqual(['v2','a',undefined,undefined]);expect(input[0].cells[0].text).toBe('a')
  expect(matchesRow(rows[0],'v2')).toBe(true);expect(labelState(rows[0],'missing')).toBe('missing');expect(labelState(rows[0],'unobserved')).toBe('unavailable')
  expect(validLabelKeys(['x:y/z.a','__proto__'])).toBe(true);expect(validLabelKeys(['x','x'])).toBe(false);expect(validLabelKeys(['a b'])).toBe(false);expect(validLabelKeys(['é'.repeat(127)])).toBe(false)
 })
 it('sorts and filters all loaded rows while keeping the rendered subset virtual and hide separate',()=>{
  const cols=columnsFor(base,preset),input=Array.from({length:2000},(_,i)=>row(`p${i.toString().padStart(4,'0')}`,i===1999?'needle':'ordinary')),rows=rowsFor(input,base,cols,preset)
  const props={columns:cols,rows,hideScope:false,filter:'',selected:null,onSelect:vi.fn()}
  const {rerender}=render(<ResourceTable {...props}/>)
  expect(screen.getAllByRole('row').length).toBeLessThan(100);expect(screen.queryByRole('columnheader',{name:/Age/})).not.toBeInTheDocument()
  rerender(<ResourceTable {...props} filter="needle"/>);expect(screen.getByText('p1999')).toBeVisible()
  rerender(<ResourceTable {...props} requestedSort={{col:labelID('x:y/z.a'),desc:false,seq:1}}/>);
  expect(within(screen.getAllByRole('row')[1]).getAllByRole('gridcell')[1]).toHaveTextContent('p1999')
  fireEvent.click(screen.getByRole('columnheader',{name:/x:y\/z.a/}))
 })
})
