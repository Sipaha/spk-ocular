import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,beforeEach,it,expect,vi} from 'vitest'
import {ResourceCertificate} from './ResourceCertificate'
import type {CertificateStatus} from './types'
import {fakeClient} from '../test/fakeClient'
import {ViewConnectionContext} from '../views/useView'
import {setLanguage} from '../i18n'
beforeEach(()=>setLanguage('en'));afterEach(cleanup)
const subject={provider:'kubernetes',target:'t',kind:'cert-manager.io/certificates',scope:'blue',name:'web-tls',uid:'cert-uid'}
const snapshot:CertificateStatus={ref:subject,capturedAt:Date.parse('2026-10-21T12:00:00Z'),notBefore:{state:'reported',value:'2026-10-01T00:00:00Z'},notAfter:{state:'reported',value:'2026-10-25T00:00:00Z'},renewal:{state:'reported',value:'2026-10-20T00:00:00Z'},validity:'within_7_days',secretName:'web-tls',conditions:[{type:'Ready',status:'True',freshness:'stale',reason:'ReportedReady'}],requestedNames:[{type:'dnsNames',value:'web.example'}],issuer:{...subject,kind:'cert-manager.io/clusterissuers',scope:'',name:'public',uid:'issuer-uid'},issuerState:'ready',consumers:[{...subject,kind:'networking.k8s.io/ingresses',name:'entry',uid:'ing-uid'}],consumerState:'ready',truncated:false}
const props=()=>({client:fakeClient([]).client,subject,onGo:vi.fn()})
it('requires explicit admission, labels reported/desired evidence and only links known identities',async()=>{
 const p=props();vi.mocked(p.client.certificateStatus).mockResolvedValue(snapshot)
 render(<ResourceCertificate {...p}/>);expect(p.client.certificateStatus).not.toHaveBeenCalled()
 fireEvent.click(screen.getByRole('button',{name:'Load Certificate status'}));await screen.findByText('Reported expiry within 7 days')
 expect(screen.getByText('Stale generation',{exact:false})).toBeInTheDocument();expect(screen.getByText('Requested SANs · desired, not verified leaf values')).toBeInTheDocument();expect(screen.getByText('Reported renewal time has arrived; this does not prove renewal failed.')).toBeInTheDocument()
 expect(screen.queryByRole('button',{name:/secrets\/web-tls/})).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'cert-manager.io/clusterissuers/public'}));expect(p.onGo).toHaveBeenCalledWith(snapshot.issuer)
 fireEvent.click(screen.getByRole('button',{name:'networking.k8s.io/ingresses/entry'}));expect(p.onGo).toHaveBeenCalledWith(snapshot.consumers[0])
})
it('keeps denied coverage distinct from no consumers and refuses foreign namespace/target links',async()=>{
 const p=props();const data=structuredClone(snapshot);data.issuer!.target='foreign';data.consumers[0].scope='other';data.consumerState='forbidden';data.notAfter={state:'invalid'};data.truncated=true
 vi.mocked(p.client.certificateStatus).mockResolvedValue(data);render(<ResourceCertificate {...p}/>);fireEvent.click(screen.getByRole('button',{name:'Load Certificate status'}));await screen.findByText('Invalid reported data')
 expect(screen.queryByRole('button',{name:'cert-manager.io/clusterissuers/public'})).not.toBeInTheDocument();expect(screen.queryByRole('button',{name:'networking.k8s.io/ingresses/entry'})).not.toBeInTheDocument();expect(screen.queryByText('No matching Ingress TLS references in the completed observation.')).not.toBeInTheDocument();expect(screen.getByText('Evidence is incomplete or limited.')).toBeInTheDocument()
})
it('cancels late replies and requires new explicit admission after connection or UID changes',async()=>{
 const p=props();let answer:(v:CertificateStatus)=>void=()=>{};let signal:AbortSignal|undefined
 vi.mocked(p.client.certificateStatus).mockImplementation((_r,s)=>{signal=s;return new Promise(r=>{answer=r})})
 const view=render(<ViewConnectionContext value="one"><ResourceCertificate {...p}/></ViewConnectionContext>)
 fireEvent.click(screen.getByRole('button',{name:'Load Certificate status'}));await waitFor(()=>expect(p.client.certificateStatus).toHaveBeenCalledTimes(1));fireEvent.click(screen.getByRole('button',{name:'Cancel'}));expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText('Reported expiry within 7 days')).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'Load Certificate status'}));await waitFor(()=>expect(p.client.certificateStatus).toHaveBeenCalledTimes(2));view.rerender(<ViewConnectionContext value="two"><ResourceCertificate {...p}/></ViewConnectionContext>);expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText('Reported expiry within 7 days')).not.toBeInTheDocument()
 view.rerender(<ViewConnectionContext value="two"><ResourceCertificate {...p} subject={{...subject,uid:'replacement'}}/></ViewConnectionContext>);expect(p.client.certificateStatus).toHaveBeenCalledTimes(2)
})
it('rejects replaced identity and retains dated evidence on failed refresh',async()=>{
 const p=props();vi.mocked(p.client.certificateStatus).mockResolvedValue({...snapshot,ref:{...subject,uid:'wrong'}})
 render(<ResourceCertificate {...p}/>);fireEvent.click(screen.getByRole('button',{name:'Load Certificate status'}));await screen.findByRole('alert');expect(screen.queryByText('Reported expiry within 7 days')).not.toBeInTheDocument()
 vi.mocked(p.client.certificateStatus).mockResolvedValue(snapshot);fireEvent.click(screen.getByRole('button',{name:'Load Certificate status'}));await screen.findByText('Reported expiry within 7 days')
 vi.mocked(p.client.certificateStatus).mockRejectedValue(new Error('Fixture denied'));fireEvent.click(screen.getByRole('button',{name:'Load Certificate status'}));await screen.findByRole('alert');expect(screen.getByText('Reported expiry within 7 days')).toBeInTheDocument()
})
