import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest'
import {act,fireEvent,render,screen,waitFor,within} from '@testing-library/react'
const mock=vi.hoisted(()=>({call:vi.fn(),off:vi.fn()}))
vi.mock('../api/client',()=>({isDesktop:()=>true}))
vi.mock('@wailsio/runtime',()=>({Call:{ByName:mock.call},Events:{On:()=>mock.off},Clipboard:{SetText:async()=>{throw new Error('fixture uses the browser clipboard')}}}))
import {DownloadsButton} from './DownloadsButton'
import {deleteDownloadFile,dismissDownload,reloadDownloads,useDownloads,type DownloadRecord} from './store'
import {setLanguage} from '../i18n'
import {copyValue,resetCopies} from '../values/copy'

const record:DownloadRecord={id:'native-owned',name:'saved.log',path:'/fixture/downloads/saved.log',kind:'logs',status:'complete',bytes:42,startedAt:1}
beforeEach(()=>{localStorage.clear();setLanguage('en');resetCopies();mock.call.mockReset();mock.off.mockReset();useDownloads.setState({records:[],storageError:false,error:false,viewed:[]});mock.call.mockImplementation(async(name:string)=>name.endsWith('.List')?{records:[record],storageError:false}:undefined)})
afterEach(()=>vi.unstubAllGlobals())
describe('Downloads panel',()=>{
 it('freezes age at opening, hides recent time and refreshes on reopening while records still update',async()=>{
  let current=2_000_000
  const clock=vi.spyOn(Date,'now').mockImplementation(()=>current)
  try{
   const records=[{...record,id:'older',name:'older.log',startedAt:current-120000},{...record,id:'recent',name:'recent.log',startedAt:current-30000}]
   mock.call.mockResolvedValue({records,storageError:false})
   render(<DownloadsButton/>);const trigger=screen.getByRole('button',{name:'Downloads'});fireEvent.click(trigger)
   const panel=screen.getByRole('dialog');await within(panel).findByText('older.log')
   expect(within(panel).getByText('2 minutes ago')).toBeInTheDocument();expect(panel.querySelectorAll('time')).toHaveLength(1)
   current+=120000
   await act(async()=>{useDownloads.setState({records:records.map(r=>({...r,bytes:2048}))})})
   expect(within(panel).getAllByText('2 kB')).toHaveLength(2)
   expect(within(panel).getByText('2 minutes ago')).toBeInTheDocument();expect(panel.querySelectorAll('time')).toHaveLength(1)
   fireEvent.keyDown(panel,{key:'Escape'});fireEvent.click(trigger)
   const reopened=screen.getByRole('dialog');expect(within(reopened).getByText('4 minutes ago')).toBeInTheDocument();expect(within(reopened).getByText('2 minutes ago')).toBeInTheDocument();expect(reopened.querySelectorAll('time')).toHaveLength(2)
  }finally{clock.mockRestore()}
 })
 it('shows a safe localized failure reason and an honest fallback for old records',async()=>{
  mock.call.mockResolvedValue({records:[{...record,status:'failed',path:undefined,failureReason:'permission'},{...record,id:'old-error',status:'failed',path:undefined}],storageError:false})
  render(<DownloadsButton/>);fireEvent.click(screen.getByRole('button',{name:'Downloads'}));const panel=screen.getByRole('dialog')
  await within(panel).findByText('Permission to read or save the download was denied.')
  expect(within(panel).getByText('The error reason is unavailable.')).toBeInTheDocument()
  expect(within(panel).queryByRole('button',{name:'Open downloaded item'})).not.toBeInTheDocument()
 })
 it('animates active transfers, highlights unseen successes and acknowledges only visible successful results',async()=>{
  mock.call.mockResolvedValue({records:[{...record,status:'active'}],storageError:false})
  render(<DownloadsButton/>);const trigger=screen.getByRole('button',{name:'Downloads'})
  await waitFor(()=>expect(trigger).toHaveAttribute('data-active','true'))
  expect(trigger).toHaveAttribute('data-unseen','false')
  await act(async()=>{useDownloads.setState({records:[record]})})
  expect(trigger).toHaveAttribute('data-unseen','true')
  fireEvent.click(trigger);await waitFor(()=>expect(trigger).toHaveAttribute('data-unseen','false'))
  expect(localStorage.getItem('ocular-downloads-viewed-v1')).toContain(record.id)
  await act(async()=>{useDownloads.setState({records:[record,{...record,id:'new',status:'active'}]})})
  expect(useDownloads.getState().viewed).not.toContain('new')
  await act(async()=>{useDownloads.setState({records:[record,{...record,id:'new'}]})})
  await waitFor(()=>expect(useDownloads.getState().viewed).toContain('new'))
 })
 it('uses compact metadata, hides full paths and dismisses without a file deletion call',async()=>{
  const now=Date.now();mock.call.mockResolvedValue({records:[{...record,bytes:1536000,startedAt:now-3000}],storageError:false})
  render(<DownloadsButton/>);fireEvent.click(screen.getByRole('button',{name:'Downloads'}));const panel=screen.getByRole('dialog');await within(panel).findByText('saved.log')
  expect(within(panel).getByText('1.5 MB')).toHaveAttribute('title',new Date(now-3000).toLocaleString('en'));expect(panel.querySelector('time')).toBeNull()
  expect(within(panel).queryByText(record.path!)).not.toBeInTheDocument()
  mock.call.mockImplementation(async(name:string)=>name.endsWith('.List')?{records:[],storageError:false}:undefined)
  fireEvent.click(within(panel).getByRole('button',{name:'Remove from history'}));await within(panel).findByText('No downloads yet')
  expect(mock.call).toHaveBeenCalledWith(expect.stringContaining('Downloads.Dismiss'),record.id)
  expect(mock.call.mock.calls.some(([name])=>String(name).endsWith('.DeleteFile'))).toBe(false)
 })
 it('starts deletion on Cancel, cancels with Escape and only deletes the previewed ID/token after confirmation',async()=>{
  mock.call.mockImplementation(async(name:string)=>name.endsWith('.List')?{records:[record],storageError:false}:name.endsWith('.PrepareDelete')?{path:record.path,token:'owned-preview',directory:false}:undefined)
  render(<DownloadsButton/>);fireEvent.click(screen.getByRole('button',{name:'Downloads'}));const panel=screen.getByRole('dialog');await within(panel).findByText('saved.log')
  fireEvent.click(within(panel).getByRole('button',{name:'Delete downloaded item'}));await within(panel).findByText('Delete downloaded item?')
  expect(within(panel).getByRole('button',{name:'Cancel'})).toHaveFocus();expect(within(panel).getByText(record.path!)).toBeInTheDocument()
  expect(mock.call.mock.calls.some(([name])=>String(name).endsWith('.DeleteFile'))).toBe(false)
  fireEvent.keyDown(panel,{key:'Escape'});await within(panel).findByText('saved.log')
  expect(mock.call).toHaveBeenCalledWith(expect.stringContaining('Downloads.CancelDelete'),record.id,'owned-preview')
  fireEvent.click(within(panel).getByRole('button',{name:'Delete downloaded item'}));await within(panel).findByText('Delete downloaded item?')
  mock.call.mockImplementation(async(name:string)=>name.endsWith('.List')?{records:[],storageError:false}:undefined)
  fireEvent.click(within(panel).getByRole('button',{name:'Delete downloaded item'}));await within(panel).findByText('No downloads yet')
  expect(mock.call).toHaveBeenCalledWith(expect.stringContaining('Downloads.DeleteFile'),record.id,'owned-preview')
 })

 it.each(['dismiss','delete'] as const)('does not resurrect an entry from an older native snapshot after %s',async(operation)=>{
  render(<DownloadsButton/>);await waitFor(()=>expect(useDownloads.getState().records).toHaveLength(1))
  let resolve!:(value:{records:DownloadRecord[];storageError:boolean})=>void
  let first=true
  mock.call.mockImplementation(async(name:string)=>{
   if(!name.endsWith('.List'))return
   if(first){first=false;return new Promise(done=>{resolve=done})}
   return {records:[],storageError:false}
  })
  const pending=reloadDownloads();await waitFor(()=>expect(resolve).toBeTypeOf('function'))
  await act(async()=>{if(operation==='dismiss')await dismissDownload(record.id);else await deleteDownloadFile(record.id,'fixture-token')})
  expect(useDownloads.getState().records).toEqual([])
  const observed:string[][]=[];const stop=useDownloads.subscribe(state=>observed.push(state.records.map(r=>r.id)))
  await act(async()=>{resolve({records:[record],storageError:false});await pending})
  stop();expect(observed.every(ids=>!ids.includes(record.id))).toBe(true);expect(useDownloads.getState().records).toEqual([])
 })
 it('opens known IDs, reveals their folder, copies the exact path and restores focus',async()=>{
  const copy=vi.fn(async()=>{});vi.stubGlobal('navigator',{clipboard:{writeText:copy}})
  render(<DownloadsButton/>);const trigger=screen.getByRole('button',{name:/^Downloads$/});trigger.focus();fireEvent.click(trigger)
  const panel=screen.getByRole('dialog',{name:'Downloads'});await within(panel).findByText('saved.log')
  fireEvent.click(within(panel).getByRole('button',{name:'Open downloaded item'}));await waitFor(()=>expect(mock.call).toHaveBeenCalledWith(expect.stringContaining('Downloads.Open'),'native-owned'))
  await waitFor(()=>expect(within(panel).getByRole('button',{name:'Show in folder'})).toBeEnabled())
  fireEvent.click(within(panel).getByRole('button',{name:'Show in folder'}));await waitFor(()=>expect(mock.call).toHaveBeenCalledWith(expect.stringContaining('Downloads.Reveal'),'native-owned'))
  await waitFor(()=>expect(within(panel).getByRole('button',{name:'Copy path'})).toBeEnabled())
  fireEvent.click(within(panel).getByRole('button',{name:'Copy path'}));await within(panel).findByText('Path copied');expect(copy).toHaveBeenCalledWith(record.path)
  fireEvent.keyDown(panel,{key:'Escape'});expect(screen.queryByRole('dialog')).not.toBeInTheDocument();expect(trigger).toHaveFocus()
 })
 it('reports action failure, keeps the downloaded item and clears only metadata',async()=>{
  render(<DownloadsButton/>);fireEvent.click(screen.getByRole('button',{name:/^Downloads$/}));const panel=screen.getByRole('dialog');await within(panel).findByText('saved.log')
  mock.call.mockRejectedValueOnce(new Error('missing file'))
  fireEvent.click(within(panel).getByRole('button',{name:'Open downloaded item'}));await within(panel).findByRole('alert');expect(within(panel).getByText('saved.log')).toBeInTheDocument()
  mock.call.mockImplementation(async(name:string)=>name.endsWith('.List')?{records:[],storageError:false}:undefined)
  fireEvent.click(within(panel).getByRole('button',{name:'Clear history'}));await within(panel).findByText('No downloads yet')
  expect(mock.call).toHaveBeenCalledWith(expect.stringContaining('Downloads.ClearCompleted'))
 })
 it('does not offer file actions for active or interrupted downloads',async()=>{
  mock.call.mockResolvedValue({records:[{...record,status:'active',path:undefined},{...record,id:'stopped',status:'interrupted',path:undefined}],storageError:false})
  render(<DownloadsButton/>);fireEvent.click(screen.getByRole('button',{name:/^Downloads$/}));const panel=screen.getByRole('dialog');await within(panel).findByRole('progressbar')
  expect(within(panel).queryByRole('button',{name:'Open downloaded item'})).not.toBeInTheDocument()
  expect(within(panel).getByText('Interrupted')).toBeInTheDocument();expect(within(panel).getAllByText('42 B')).toHaveLength(2)
  await act(async()=>{useDownloads.setState({records:[]})})
 })
 it('supersedes an older pending Secret copy through the shared clipboard queue',async()=>{
  const copied=vi.fn(async()=>{}),oldWrite=vi.fn(async()=>{});vi.stubGlobal('navigator',{clipboard:{writeText:copied}})
  let resolve!:(text:string)=>void
  const older=copyValue({read:()=>new Promise<string>(done=>{resolve=done}),valid:()=>true,write:oldWrite})
  render(<DownloadsButton/>);fireEvent.click(screen.getByRole('button',{name:/^Downloads$/}));const panel=screen.getByRole('dialog',{name:'Downloads'});await within(panel).findByText('saved.log')
  fireEvent.click(within(panel).getByRole('button',{name:'Copy path'}));await within(panel).findByText('Path copied')
  resolve('late fixture value');expect(await older).toBe('superseded');expect(oldWrite).not.toHaveBeenCalled();expect(copied).toHaveBeenCalledExactlyOnceWith(record.path)
 })
 it.each(['success','failure'] as const)('ignores an older native snapshot %s after clearing history',async(outcome)=>{
  render(<DownloadsButton/>);fireEvent.click(screen.getByRole('button',{name:/^Downloads$/}));const panel=screen.getByRole('dialog',{name:'Downloads'});await within(panel).findByText('saved.log')
  let resolve!:(value:{records:DownloadRecord[];storageError:boolean})=>void
  let reject!:(reason:Error)=>void
  let first=true
  mock.call.mockImplementation(async(name:string)=>{
   if(!name.endsWith('.List'))return
   if(first){first=false;return new Promise((done,fail)=>{resolve=done;reject=fail})}
   return {records:[],storageError:false}
  })
  const pending=reloadDownloads();await waitFor(()=>expect(resolve).toBeTypeOf('function'))
  fireEvent.click(within(panel).getByRole('button',{name:'Clear history'}));await within(panel).findByText('No downloads yet')
  const observed:string[][]=[],errors:boolean[]=[];const stop=useDownloads.subscribe(state=>{observed.push(state.records.map(r=>r.id));errors.push(state.error)})
  await act(async()=>{if(outcome==='success')resolve({records:[record],storageError:false});else reject(new Error('older snapshot failed'));await pending})
  stop();expect(observed.every(ids=>!ids.includes(record.id))).toBe(true)
  expect(useDownloads.getState().records).toEqual([])
  expect(errors).not.toContain(true)
 })
})
