import {act, fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, expect, it, vi} from 'vitest'
import KrrDialog from './KrrDialog'
import {fixture, storedFixture} from './fixture'
import {MAX_BYTES, type KrrResponse} from './model'
import {fakeClient} from '../test/fakeClient'

let readers: Reader[] = []
class Reader {
  result: ArrayBuffer | null = null
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  abort = vi.fn()
  readAsArrayBuffer = vi.fn()
  constructor() { readers.push(this) }
  finish(data = fixture()) { const bytes = new TextEncoder().encode(JSON.stringify(data)); this.result = new ArrayBuffer(bytes.length); new Uint8Array(this.result).set(bytes); this.onload?.() }
}
afterEach(() => { vi.unstubAllGlobals(); readers = [] })
function setup(client = fakeClient([]).client) {
  vi.stubGlobal('FileReader', Reader)
  const ui = render(<KrrDialog client={client} onClose={vi.fn()}/>)
  const input = screen.getByLabelText('Choose JSON report') as HTMLInputElement
  fireEvent.click(screen.getByRole('checkbox'))
  return {...ui, input, client}
}
const choose = (input: HTMLInputElement, size = 100) => fireEvent.change(input, {target: {files: [new File(['x'.repeat(size)], 'private-name.json')]}})
it('requires operator assertion and rejects oversize before reading or backend import', async () => {
  const client=fakeClient([]).client;client.krr=vi.fn(async()=>({reports:[]}));vi.stubGlobal('FileReader',Reader);render(<KrrDialog client={client} onClose={vi.fn()}/>);const input=screen.getByLabelText('Choose JSON report') as HTMLInputElement
  expect(input).toBeDisabled();fireEvent.click(screen.getByRole('checkbox'));choose(input,MAX_BYTES+1);expect(readers).toHaveLength(0);expect(screen.getByRole('alert')).toBeInTheDocument();await waitFor(()=>expect(client.krr).toHaveBeenCalledTimes(1));expect(client.krr).toHaveBeenCalledWith({command:'status'},expect.any(AbortSignal))
})
it('submits bytes only to the backend and renders returned projection literally', async()=>{
 const client=fakeClient([]).client,stored=storedFixture();stored.report.rows[0].name='<script>literal</script> https://example.invalid'
 client.krr=vi.fn(async req=>req.command==='status'?{reports:[]}:{reports:[stored.summary],selected:stored})
 const {input}=setup(client);choose(input);act(()=>readers[0].finish());await waitFor(()=>expect(screen.getByText(/<script>literal/u)).toBeInTheDocument())
 const call=vi.mocked(client.krr).mock.calls.find(c=>c[0].command==='import')![0];expect(JSON.parse(atob(call.data!))).toEqual(fixture());expect(call).not.toHaveProperty('filename');expect(document.body.textContent).not.toContain('PRIVATE-');expect(document.body.textContent).not.toContain('private-name.json');expect(screen.queryByRole('link')).not.toBeInTheDocument()
})
it('fetches backend-owned reports after panel remount and explicitly forgets them',async()=>{
 const client=fakeClient([]).client,stored=storedFixture();client.krr=vi.fn(async req=>req.command==='get'?{reports:[stored.summary],selected:stored}:req.command==='forget'?{reports:[]}:{reports:[stored.summary]})
 const ui=setup(client);await waitFor(()=>expect(screen.getByRole('button',{name:'Open report'})).toBeInTheDocument());fireEvent.click(screen.getByRole('button',{name:'Open report'}));await waitFor(()=>expect(screen.getByText(/context-a · Deployment/u)).toBeInTheDocument());ui.unmount()
 setup(client);await waitFor(()=>expect(screen.getByRole('button',{name:'Open report'})).toBeInTheDocument());expect(screen.queryByText(/context-a · Deployment/u)).not.toBeInTheDocument();fireEvent.click(screen.getByRole('button',{name:'Discard report'}));await waitFor(()=>expect(client.krr).toHaveBeenCalledWith({command:'forget',id:'test-report'},expect.any(AbortSignal)));await waitFor(()=>expect(screen.getByRole('button',{name:/^Close$/})).toHaveFocus())
})
it('replacement, Cancel and disposal abort reads and fence late backend answers',async()=>{
 const client=fakeClient([]).client,stored=storedFixture();let finish!:()=>void;let signal:AbortSignal|undefined
 client.krr=vi.fn((req,s)=>req.command==='status'?Promise.resolve({reports:[]}):new Promise<KrrResponse>(resolve=>{signal=s;finish=()=>resolve({reports:[stored.summary],selected:stored})}))
 const {input,unmount}=setup(client);choose(input);fireEvent.click(screen.getByRole('button',{name:'Cancel'}));expect(readers[0].abort).toHaveBeenCalled();act(()=>readers[0].finish());expect(vi.mocked(client.krr).mock.calls.filter(c=>c[0].command==='import')).toHaveLength(0)
 choose(input);act(()=>readers[1].finish());await waitFor(()=>expect(finish).toBeDefined());fireEvent.click(screen.getByRole('button',{name:'Cancel'}));expect(signal?.aborted).toBe(true);await act(async()=>finish());expect(screen.queryByText(/context-a · Deployment/u)).not.toBeInTheDocument()
 choose(input);unmount();expect(readers[2].abort).toHaveBeenCalled();act(()=>readers[2].finish())
})
it('shows a rejected backend import and never retains previous selected evidence',async()=>{
 const client=fakeClient([]).client;let reject=false;const stored=storedFixture()
 client.krr=vi.fn(async req=>{if(req.command==='status')return {reports:[]};if(reject)throw new Error('rejected');return {reports:[stored.summary],selected:stored}})
 const {input}=setup(client);choose(input);act(()=>readers[0].finish());await waitFor(()=>expect(screen.getByText(/context-a · Deployment/u)).toBeInTheDocument())
 reject=true;choose(input);act(()=>readers[1].finish());await waitFor(()=>expect(screen.getByRole('alert')).toBeInTheDocument());expect(screen.queryByText(/context-a · Deployment/u)).not.toBeInTheDocument()
})

it('restores modal focus after opening a retained report so Escape still closes it',async()=>{
 const client=fakeClient([]).client,stored=storedFixture();let finish!:(r:KrrResponse)=>void
 client.krr=vi.fn(req=>req.command==='get'?new Promise<KrrResponse>(resolve=>{finish=resolve}):Promise.resolve({reports:[stored.summary]}))
 const close=vi.fn();render(<KrrDialog client={client} onClose={close}/>);await waitFor(()=>expect(screen.getByRole('button',{name:'Open report'})).toBeInTheDocument())
 const open=screen.getByRole('button',{name:'Open report'});open.focus();fireEvent.click(open);open.blur()
 await act(async()=>finish({reports:[stored.summary],selected:stored}));expect(screen.getByRole('button',{name:/^Close$/})).toHaveFocus()
 fireEvent.keyDown(document.activeElement!,{key:'Escape'});expect(close).toHaveBeenCalledTimes(1)
})
