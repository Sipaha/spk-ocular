import {Component,lazy,Suspense,type ReactNode} from 'react'
const Viewer=lazy(()=>import('../components/YamlView'))

/** Locally authored evidence must remain readable even when a browser cannot
 * fetch the optional highlighting/editor chunk. Keep heavy code out of entry. */
class ViewerBoundary extends Component<{children:ReactNode;fallback:ReactNode},{failed:boolean}>{
 state={failed:false}
 static getDerivedStateFromError(){return {failed:true}}
 render(){return this.state.failed?this.props.fallback:this.props.children}
}
export function LearningYaml({text,label,documentId}:{text:string;label:string;documentId:string}){
 const plain=<pre data-learning-yaml-plain aria-label={label} tabIndex={0} className="h-full overflow-auto bg-app p-2 font-mono text-[13px] leading-relaxed">{text}</pre>
 return <ViewerBoundary fallback={plain}><Suspense fallback={plain}><Viewer text={text} label={label} documentId={documentId}/></Suspense></ViewerBoundary>
}
