import {act, cleanup, fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, beforeEach, expect, it, vi} from 'vitest'
import {ManagementReview} from './ManagementReview'
import {matchingControllers, validSubject} from './model'
import {context, where, application, controller, argo, flux} from './fixtures'
import {connectedClient, k8s} from '../test/fakeClient'
import {initialState, useStore} from '../store'
import {useDock} from '../dock/store'
import {setLanguage, t} from '../i18n'
import {ViewConnectionContext} from '../views/useView'
import type {ArgoSnapshot} from '../argocd/types'

beforeEach(() => {setLanguage('en'); useStore.setState({...initialState}); useDock.setState({tabs:[], active:null})})
afterEach(cleanup)
async function fixture() {
 const f = connectedClient([k8s('prod',{configRev:'r1'})]); useStore.setState({view:await f.client.listTargets()})
 vi.mocked(f.client.argoCD).mockResolvedValue(argo); vi.mocked(f.client.fluxCD).mockResolvedValue(flux)
 return f
}
const load = () => fireEvent.click(screen.getByRole('button',{name:t('management.load')}))
const namespace = () => screen.getByRole('textbox',{name:t('management.namespace')})
it('loads exactly one explicit namespace; renders controller/source failures and pins exact UIDs without closing the review', async () => {
 const f=await fixture(); render(<ManagementReview client={f.client} where={where} context={context}/>)
 expect(f.client.argoCD).not.toHaveBeenCalled(); expect(f.client.fluxCD).not.toHaveBeenCalled()
 load(); await screen.findByText('ComparisonError: Manifest comparison failed')
 expect(screen.getByText(/GitOperationFailed: Repository fetch failed/)).toBeVisible()
 expect(screen.getByText(/OutOfSync/)).toHaveTextContent('Degraded')
 expect(screen.getByText(t('management.notice'))).toBeVisible()
 expect(screen.getByRole('status')).toHaveTextContent(t('flux.unverified'))
 expect(screen.queryByText(t('argo.managedBy'),{exact:true})).not.toBeInTheDocument()
 expect(f.client.argoCD).toHaveBeenCalledWith({provider:'kubernetes',target:'prod',scope:{mode:'one',name:'gitops'}},expect.any(AbortSignal))
 expect(f.client.fluxCD).toHaveBeenCalledWith({provider:'kubernetes',target:'prod',scope:{mode:'one',name:'gitops'}},expect.any(AbortSignal))
 fireEvent.click(screen.getByRole('button',{name:/Pin properties · argoproj/}))
 expect(useDock.getState().tabs[0]).toMatchObject({kind:'properties',ref:application.ref})
 expect(screen.getByRole('region',{name:t('management.title')})).toBeVisible()
 expect(f.client.getResource).not.toHaveBeenCalled(); expect(f.client.runAction).not.toHaveBeenCalled(); expect(f.client.runEdit).not.toHaveBeenCalled()
 fireEvent.change(namespace(),{target:{value:'other'}})
 expect(screen.queryByText(/Manifest comparison failed/)).not.toBeInTheDocument()
 expect(f.client.argoCD).toHaveBeenCalledTimes(1)
 load();await waitFor(()=>expect(f.client.argoCD).toHaveBeenCalledTimes(2))
 expect(vi.mocked(f.client.argoCD).mock.calls[1][0].scope).toEqual({mode:'one',name:'other'})
})
it('keeps independent read errors, missing API, empty and partial coverage visible with no manager certification',async()=>{
 const f=await fixture();vi.mocked(f.client.argoCD).mockRejectedValue(new Error('Access denied'))
 vi.mocked(f.client.fluxCD).mockResolvedValue({...flux,discovery:'partial',coverage:[{kind:'Kustomization',scope:'gitops',state:'denied'},{kind:'GitRepository',scope:'gitops',state:'unavailable'},{kind:'HelmRelease',scope:'gitops',state:'empty'}]})
 render(<ManagementReview client={f.client} where={where} context={context}/>);load()
 await screen.findByRole('alert');expect(screen.getByRole('alert')).toHaveTextContent('Access denied')
 expect(screen.getByText(/Kustomization \/ gitops · denied/)).toBeInTheDocument()
 expect(screen.getByText(/GitRepository \/ gitops · unavailable/)).toBeInTheDocument()
 expect(screen.getByText(/HelmRelease \/ gitops · empty/)).toBeInTheDocument()
 expect(screen.queryByRole('button',{name:/Pin properties/})).not.toBeInTheDocument()
})
it('aborts cancelled and changed-namespace reads; ignores late answers and requires another explicit load',async()=>{
 const f=await fixture();let resolve!:(value:ArgoSnapshot)=>void;let signal:AbortSignal|undefined
 vi.mocked(f.client.argoCD).mockImplementation((_req,s)=>{signal=s;return new Promise(done=>{resolve=done})})
 render(<ManagementReview client={f.client} where={where} context={context}/>);load();await waitFor(()=>expect(signal).toBeDefined())
 fireEvent.click(screen.getByRole('button',{name:t('view.loadCancel')}));expect(signal!.aborted).toBe(true)
 await act(async()=>resolve(argo));expect(screen.queryByText(/Manifest comparison failed/)).not.toBeInTheDocument()
 fireEvent.change(namespace(),{target:{value:'new'}});expect(f.client.argoCD).toHaveBeenCalledTimes(1)
})
it('does not relabel evidence or admit loading on a replacement connection, configuration or subject',async()=>{
 const f=await fixture();let resolve!:(value:ArgoSnapshot)=>void;let signal:AbortSignal|undefined
 vi.mocked(f.client.argoCD).mockImplementation((_r,s)=>{signal=s;return new Promise(done=>{resolve=done})})
 const view=render(<ViewConnectionContext value="first"><ManagementReview client={f.client} where={where} context={context}/></ViewConnectionContext>)
 load();await waitFor(()=>expect(signal).toBeDefined())
 view.rerender(<ViewConnectionContext value="replacement"><ManagementReview client={f.client} where={where} context={context}/></ViewConnectionContext>)
 expect(signal!.aborted).toBe(true);expect(screen.getByRole('button',{name:t('management.load')})).toBeDisabled()
 await act(async()=>resolve(argo));expect(screen.queryByText(/Manifest comparison failed/)).not.toBeInTheDocument()
 expect(screen.getByText(t('management.stale'))).toBeVisible()
})
it('rejects empty/invalid namespace, config replacement and identity mismatch without inventory reads',async()=>{
 const f=await fixture();const v=render(<ManagementReview client={f.client} where={where} context={context}/>)
 for(const value of ['', 'all namespaces', '*', 'a'.repeat(64)]){fireEvent.change(namespace(),{target:{value}});expect(screen.getByRole('button',{name:t('management.load')})).toBeDisabled()}
 act(()=>useStore.setState({view:{...f.state.view,groups:f.state.view.groups.map(g=>({...g,targets:g.targets.map(target=>({...target,configRev:'r2'}))}))}}))
 expect(screen.getByText(t('management.stale'))).toBeVisible()
 v.rerender(<ManagementReview client={f.client} where={{...where,ref:{...where.ref,uid:'replacement'}}} context={context}/>)
 expect(screen.getByRole('button',{name:t('management.load')})).toBeDisabled();expect(f.client.argoCD).not.toHaveBeenCalled()
})
it('never certifies truncated, ambiguous, remote, installation or mismatched metadata',()=>{
 expect(validSubject(context,where)).toBe(true)
 expect(validSubject(context,{...where,ref:{...where.ref,uid:'old'}})).toBe(false)
 const match=(c=context,a=argo,f=flux)=>matchingControllers(c,a,f)
 expect(match().argo).toHaveLength(1);expect(match().flux).toHaveLength(1)
 expect(match({...context,truncated:true})).toEqual({argo:[],flux:[]})
 expect(match({...context,argoTracking:{...context.argoTracking!,installation:'other'}}).argo).toEqual([])
 for(const extra of [{truncated:true},{discovery:'partial'},{coverage:[]},{coverage:[{kind:'Application',scope:'gitops',state:'denied'}]}])expect(match(context,{...argo,...extra}).argo).toEqual([])
 expect(match(context,{...argo,objects:[application,{...application,ref:{...application.ref,uid:'other-app'}}]}).argo).toHaveLength(2)
 for(const extra of [{truncated:true},{localDestination:false},{ref:{...application.ref,target:'other'}},{ref:{...application.ref,kind:'other/applications'}},{resources:[{...application.resources[0],group:'other'}]},{resources:[{...application.resources[0],namespace:'other'}]}])expect(match(context,{...argo,objects:[{...application,...extra}]}).argo).toEqual([])
 for(const extra of [{truncated:true},{localDestination:false},{ref:{...controller.ref,scope:'other'}},{ref:{...controller.ref,kind:'other/kustomizations'}}])expect(match(context,argo,{...flux,objects:[{...controller,...extra}]}).flux).toEqual([])
 expect(match({...context,fluxTracking:{...context.fluxTracking!,candidates:[{...context.fluxTracking!.candidates[0],uid:'old-controller'}]}}).flux).toEqual([])
})
it('has every review label in all eight locales without fallback',async()=>{
 const {languages}=await import('../languages'),{loadLanguage,_dicts}=await import('../i18n')
 for(const language of languages){await loadLanguage(language);for(const key of ['title','notice','namespace','load','stale','pin','pinNotice','pinFailed'])expect(_dicts[language]?.[`management.${key}` as keyof typeof _dicts.en]).toBeTruthy()}
})
it('namespace Enter does not submit an enclosing scale form and cancellation restores load focus',async()=>{
 const f=await fixture(),submit=vi.fn((e:{preventDefault:()=>void})=>e.preventDefault())
 vi.mocked(f.client.argoCD).mockReturnValue(new Promise(()=>{}))
 render(<form onSubmit={submit}><ManagementReview client={f.client} where={where} context={context}/></form>)
 fireEvent.keyDown(namespace(),{key:'Enter'});expect(submit).not.toHaveBeenCalled();expect(f.client.argoCD).not.toHaveBeenCalled()
 const button=screen.getByRole('button',{name:t('management.load')});button.focus();load()
 await waitFor(()=>expect(f.client.argoCD).toHaveBeenCalledTimes(1))
 expect(screen.getByRole('button',{name:t('app.loading')})).toHaveFocus()
 fireEvent.click(screen.getByRole('button',{name:t('app.loading')}));expect(f.client.argoCD).toHaveBeenCalledTimes(1)
 fireEvent.click(screen.getByRole('button',{name:t('view.loadCancel')}));expect(screen.getByRole('button',{name:t('management.load')})).toHaveFocus()
})
