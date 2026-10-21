import type {Ref} from '../api/types'
export interface HistorySource {url:string;token?:string;dedicated:boolean;clusterLabel:string;clusterValue:string}
export interface HistoryRequest {ref:Ref;source:HistorySource;window:string}
export interface HistorySeries {container:string;incarnation:string;values:(number|null)[]}
export interface HistoryMetric {name:'cpu'|'memory'|'throttling';state:string;series:HistorySeries[];missingContainers?:string[]}
export interface HistoryDeclaration {container:string;cpuRequest:number|null;cpuLimit:number|null;memoryRequest:number|null;memoryLimit:number|null}
export interface MetricsHistory {ref:Ref;resourceVersion:string;source:string;scope:string;capturedAt:number;requestedStart:number;start:number;end:number;step:number;metrics:HistoryMetric[];declarations:HistoryDeclaration[]}
