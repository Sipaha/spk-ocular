import {act,render,screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {it,expect,vi} from 'vitest'
import {connectedClient,k8s} from '../test/fakeClient'
import {ViewConnectionContext} from '../views/useView'
import {FluxWorkloadStatus} from './FluxWorkloadStatus'
import {evidenceKey,shareEvidence} from './evidence'
import type {FluxSnapshot,FluxObject} from './types'
import type {Row} from '../api/types'
const object:FluxObject={ref:{provider:'kubernetes',target:'prod',scope:'ops',kind:'kustomize.toolkit.fluxcd.io/kustomizations',name:'delivery',uid:'controller-uid'},kind:'Kustomization',section:'Workloads',state:'Ready',message:'',generation:2,observedGeneration:2,conditions:[],url:'',path:'',revision:'',links:[],inventory:[{group:'apps',kind:'Deployment',version:'v1',namespace:'blue',name:'web'}],localDestination:true,createdAt:0,truncated:false}
const row:Row={id:'uid-web',ref:{provider:'kubernetes',target:'prod',scope:'blue',kind:'apps/deployments',name:'web',uid:'uid-web'},health:{state:'unknown'},cells:[],fluxTracking:{group:'apps',kind:'Deployment',candidates:[{kind:'Kustomization',namespace:'ops',name:'delivery'}]}}
const snapshot:FluxSnapshot={objects:[object],events:[],coverage:[],discovery:'ready',capturedAt:0,truncated:false}
it('uses only exact loaded connection/scope evidence and never starts an inventory read for a workload cell',async()=>{
 const f=connectedClient([k8s('prod')]),onGo=vi.fn(),scope={mode:'all' as const}
 const view=(connection:string,selection=scope)=><ViewConnectionContext value={connection}><FluxWorkloadStatus client={f.client} resource={row} scope={selection} onGo={onGo}/></ViewConnectionContext>
 const r=render(view('one'));expect(screen.getByText('Unknown')).toBeInTheDocument();expect(f.client.fluxCD).not.toHaveBeenCalled()
 act(()=>shareEvidence(f.client,evidenceKey('kubernetes','prod','one',scope),snapshot))
 await userEvent.click(screen.getByRole('button',{name:'Ready · delivery'}));expect(onGo).toHaveBeenCalledWith(object.ref)
 r.rerender(view('two'));expect(screen.queryByRole('button')).not.toBeInTheDocument();expect(screen.getByText('Unknown')).toBeInTheDocument();expect(f.client.fluxCD).not.toHaveBeenCalled()
 r.rerender(view('one'));act(()=>shareEvidence(f.client,evidenceKey('kubernetes','prod','one',scope),{...snapshot,objects:[{...object,localDestination:false}]}));expect(screen.queryByRole('button')).not.toBeInTheDocument()
})
