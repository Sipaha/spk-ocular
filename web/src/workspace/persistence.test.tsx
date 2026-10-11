import {act,renderHook,waitFor} from '@testing-library/react'
import {beforeEach,expect,it,vi} from 'vitest'
import {fakeClient} from '../test/fakeClient'
import {dock,useDock} from '../dock/store'
import {useStore} from '../store'
import {usePanelWidths} from '../components/PanelResize'
import {useWorkspacePersistence,useWorkspaceStatus} from './persistence'
import {WORKSPACE_KEY,parseWorkspace,serializeWorkspace} from './snapshot'
const ref={provider:'kubernetes',target:'prod',kind:'pods',name:'web',uid:'one'}
beforeEach(()=>{useDock.setState({tabs:[],active:null,height:320});useWorkspaceStatus.setState({ready:false,error:null});useStore.setState({view:null});usePanelWidths.setState({targets:240,navigation:192,details:null,helmDetails:null,agentsTargets:210})})
it('does not overwrite saved metadata after a failed load; explicit retry resumes saving',async()=>{
 const {client}=fakeClient([])
 vi.mocked(client.getTargetState).mockRejectedValueOnce(new Error('unavailable'))
 renderHook(()=>useWorkspacePersistence(client))
 await waitFor(()=>expect(useWorkspaceStatus.getState().error).toBe('load'))
 act(()=>usePanelWidths.setState({targets:260}));expect(client.setTargetState).not.toHaveBeenCalled()
 act(()=>useWorkspaceStatus.getState().retry());await waitFor(()=>expect(useWorkspaceStatus.getState().ready).toBe(true))
 act(()=>dock.openProperties({provider:'kubernetes',id:'prod'},'prod',ref))
 await waitFor(()=>expect(client.setTargetState).toHaveBeenCalled())
})
it('serializes slow writes, keeps the latest snapshot, and makes save failures retryable',async()=>{
 const {client}=fakeClient([]);let finish:()=>void=()=>{}
 vi.mocked(client.setTargetState).mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve})).mockRejectedValueOnce(new Error('disk full'))
 renderHook(()=>useWorkspacePersistence(client));await waitFor(()=>expect(useWorkspaceStatus.getState().ready).toBe(true))
 act(()=>dock.openProperties({provider:'kubernetes',id:'prod'},'prod',ref))
 await waitFor(()=>expect(client.setTargetState).toHaveBeenCalledTimes(1))
 act(()=>dock.close(useDock.getState().active!));expect(client.setTargetState).toHaveBeenCalledTimes(1)
 await act(async()=>finish());await waitFor(()=>expect(useWorkspaceStatus.getState().error).toBe('save'))
 act(()=>useWorkspaceStatus.getState().retry());await waitFor(()=>expect(useWorkspaceStatus.getState().error).toBeNull())
 const last=vi.mocked(client.setTargetState).mock.calls.at(-1)!
 expect(last[2]).toBe(WORKSPACE_KEY);expect(parseWorkspace(last[3])?.panels).toEqual([])
})
it('never clobbers a resize performed during load and rejects an invalid snapshot until explicit reset',async()=>{
 const {client}=fakeClient([]);let done:(v:Record<string,string>)=>void=()=>{}
 vi.mocked(client.getTargetState).mockImplementationOnce(()=>new Promise(resolve=>{done=resolve}))
 renderHook(()=>useWorkspacePersistence(client))
 act(()=>usePanelWidths.setState({targets:300}))
 await act(async()=>done({[WORKSPACE_KEY]:'{"v":999}'}));expect(useWorkspaceStatus.getState().error).toBe('invalid');expect(client.setTargetState).not.toHaveBeenCalled()
 act(()=>useWorkspaceStatus.getState().reset());await waitFor(()=>expect(client.setTargetState).toHaveBeenCalled())
 expect(usePanelWidths.getState().targets).toBe(300)
})

it('migrates a legacy selected-target height without connecting',async()=>{
 const {client,state}=fakeClient([])
 state.view.selected={provider:'kubernetes',id:'prod'}
 vi.mocked(client.getTargetState).mockResolvedValueOnce({}).mockResolvedValueOnce({logsHeight:'460'})
 renderHook(()=>useWorkspacePersistence(client))
 await waitFor(()=>expect(useWorkspaceStatus.getState().ready).toBe(true))
 expect(useDock.getState().height).toBe(460);expect(client.connectTarget).not.toHaveBeenCalled();expect(client.openTerminal).not.toHaveBeenCalled()
})

it('a successful late restore cannot overwrite a resize made while loading',async()=>{
 const {client}=fakeClient([])
 const stored=serializeWorkspace(useDock.getState(),usePanelWidths.getState())!
 let done:(v:Record<string,string>)=>void=()=>{}
 vi.mocked(client.getTargetState).mockImplementationOnce(()=>new Promise(resolve=>{done=resolve}))
 renderHook(()=>useWorkspacePersistence(client))
 act(()=>usePanelWidths.setState({targets:320}))
 await act(async()=>done({[WORKSPACE_KEY]:stored}))
 await waitFor(()=>expect(useWorkspaceStatus.getState().ready).toBe(true))
 expect(usePanelWidths.getState().targets).toBe(320)
 await waitFor(()=>expect(client.setTargetState).toHaveBeenCalled())
 expect(parseWorkspace(vi.mocked(client.setTargetState).mock.calls.at(-1)![3])?.widths.targets).toBe(320)
})

it('explicit reset invalidates an older pending restore response',async()=>{
 const {client}=fakeClient([])
 dock.openProperties({provider:'kubernetes',id:'old'},'old',{...ref,target:'old'})
 const stored=serializeWorkspace(useDock.getState(),usePanelWidths.getState())!
 useDock.setState({tabs:[],active:null})
 let done:(v:Record<string,string>)=>void=()=>{}
 vi.mocked(client.getTargetState).mockImplementationOnce(()=>new Promise(resolve=>{done=resolve}))
 renderHook(()=>useWorkspacePersistence(client))
 act(()=>useWorkspaceStatus.getState().reset())
 await act(async()=>done({[WORKSPACE_KEY]:stored}))
 expect(useDock.getState().tabs).toEqual([])
 expect(useWorkspaceStatus.getState().ready).toBe(true)
})
