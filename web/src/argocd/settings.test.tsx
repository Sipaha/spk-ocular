import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,beforeEach,it,expect,vi} from 'vitest'
import ArgoWorkspace from './ArgoWorkspace'
import {ArgoManagedBy} from './ArgoManagedBy'
import {fakeClient} from '../test/fakeClient'
import {ViewHub} from '../views/viewSync'
import {ViewConnectionContext} from '../views/useView'
import {setLanguage} from '../i18n'
import type {Resource} from '../api/types'
import type {ArgoObject,ArgoSnapshot} from './types'
import {evidenceKey,readEvidence,shareEvidence} from './evidence'
beforeEach(()=>setLanguage('en'));afterEach(cleanup)
it('opens a separate Settings page without a Secret-backed read until the operator loads it',async()=>{
 const {client}=fakeClient([]),target={provider:'synthetic',id:'demo'},scope={mode:'one' as const,name:'blue'}
 render(<ArgoWorkspace client={client} target={target} scope={scope} workspace="argocd-repositories" kinds={[]} scopePicker={null} drawer={{client,hub:new ViewHub(client),target}}/>)
 expect(client.argoCD).not.toHaveBeenCalled();expect(client.getResource).not.toHaveBeenCalled()
 fireEvent.click(screen.getByRole('button',{name:'Load registered settings'}))
 await waitFor(()=>expect(client.argoCD).toHaveBeenCalledWith({provider:'synthetic',target:'demo',scope,settings:'repository'},expect.any(AbortSignal)))
 expect(client.getResource).not.toHaveBeenCalled()
})
const resource:Resource={ref:{provider:'synthetic',target:'demo',kind:'crates',scope:'blue',name:'alpha',uid:'current-alpha'},argoTracking:{application:'commerce',method:'annotation',group:'',kind:'Crate',installation:''},health:{state:'unknown'},facts:[],yaml:'',relations:null}
const app:ArgoObject={ref:{provider:'synthetic',target:'demo',kind:'argoproj.io/applications',scope:'blue',name:'commerce',uid:'app-current'},kind:'Application',project:'default',sync:'Synced',health:'Healthy',operation:'Unknown',operationMessage:'',destination:'https://kubernetes.default.svc',destinationNamespace:'blue',localDestination:true,sources:[],conditions:[],resources:[{group:'',version:'v1',kind:'Crate',namespace:'blue',name:'alpha',sync:'Synced',health:'Healthy',message:''}],generators:[],description:'',createdAt:0,reconciledAt:0,truncated:false}
const snapshot:ArgoSnapshot={objects:[app],servers:[],events:[],coverage:[],discovery:'ready',truncated:false,capturedAt:1}
it('uses only evidence loaded for the exact connection and selection, and never caches settings',()=>{
 const {client}=fakeClient([]),scope={mode:'one' as const,name:'blue'},key=evidenceKey('synthetic','demo','1',scope)
 shareEvidence(client,key,snapshot);expect(readEvidence(client,key)).toBe(snapshot)
 expect(readEvidence(client,evidenceKey('synthetic','demo','2',scope))).toBeNull()
 expect(readEvidence(client,evidenceKey('synthetic','demo','1',{mode:'all'}))).toBeNull()
 shareEvidence(client,'secret-settings',{...snapshot,settings:[]});expect(readEvidence(client,'secret-settings')).toBeNull()
 const onGo=vi.fn();render(<ViewConnectionContext value="1"><ArgoManagedBy client={client} resource={resource} scope={scope} onGo={onGo}/></ViewConnectionContext>)
 fireEvent.click(screen.getByRole('button',{name:'Application / blue/commerce'}));expect(onGo).toHaveBeenCalledWith(app.ref);expect(client.argoCD).not.toHaveBeenCalled()
})
it('a generic instance label without reported membership stays unverified',async()=>{
 const {client}=fakeClient([]);vi.mocked(client.argoCD).mockResolvedValue({...snapshot,objects:[{...app,resources:[]}]})
 render(<ArgoManagedBy client={client} resource={{...resource,argoTracking:{...resource.argoTracking!,method:'label'}}} scope={{mode:'one',name:'blue'}} onGo={vi.fn()}/>)
 expect(screen.queryByRole('button',{name:/Application \//})).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'Find managing Application'}))
 await waitFor(()=>expect(screen.getByText('The managing Application could not be verified in this selection and instance.')).toBeVisible())
 expect(screen.queryByRole('button',{name:/Application \//})).not.toBeInTheDocument()
})
