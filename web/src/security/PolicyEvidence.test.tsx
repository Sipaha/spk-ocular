import {cleanup,render,screen} from '@testing-library/react'
import {afterEach,beforeEach,expect,it} from 'vitest'
import type {SecurityFinding} from '../api/types'
import {setLanguage} from '../i18n'
import {PolicyEvidence} from './PolicyEvidence'

beforeEach(()=>setLanguage('en'));afterEach(cleanup)
const finding:SecurityFinding={id:'source',ref:{provider:'kubernetes',target:'a',kind:'openreports.io/reports',name:'source',scope:'blue',uid:'report-uid'},kindTitle:'Report',category:'policy',severity:'high',rule:'policy / rule',evidence:'Report',policy:{result:'skip',nativeResult:'skip',nativeSeverity:'HIGH',source:'producer',policy:'policy',rule:'rule',category:'Reliability',scored:false,message:'<script>literal</script> https://example.invalid/',process:'background scan',exceptions:'reported-exception',apiVersion:'openreports.io/v1alpha1',resourceVersion:'9',scope:{apiVersion:'v1',kind:'Pod',name:'same-name',namespace:'blue',uid:'old-subject'},subjects:[],scopeSelector:'',subjectSelector:'app=old',maxResults:20,statusFilter:['skip'],summary:{skip:1}}}
it('keeps skipped evidence, exceptions, source identity and descriptive subject separate without executable text',()=>{
 const view=render(<PolicyEvidence finding={finding}/>)
 expect(view.container).toHaveTextContent('Skipped (reported)');expect(view.container).toHaveTextContent('reported-exception')
 expect(view.container).toHaveTextContent('report-uid · RV 9');expect(view.container).toHaveTextContent('old-subject')
 expect(view.container).toHaveTextContent('<script>literal</script> https://example.invalid/')
 expect(view.container.querySelector('script,a')).toBeNull();expect(screen.queryByRole('button')).toBeNull()
 expect(view.container).toHaveTextContent('Reported scored flag: False');expect(view.container).toHaveTextContent('Report scope (unverified)')
 expect(view.container).toHaveTextContent('Skips and missing results never establish a pass')
})
it('does not invent scored, timestamp, limits, summary or exception evidence when optional fields are absent',()=>{
 const f={...finding,scannedAt:0,policy:{...finding.policy!,scored:null,source:'',exceptions:'',maxResults:null,statusFilter:[],summary:null,scope:null}}
 const view=render(<PolicyEvidence finding={f}/>)
 expect(view.container).toHaveTextContent('Reported scored flag: Unknown')
 expect(view.container).toHaveTextContent('Result time: Unknown');expect(view.container).toHaveTextContent('Reported summary: Unknown')
 expect(view.container).toHaveTextContent('Reported exceptions (unverified): Unknown')
})
