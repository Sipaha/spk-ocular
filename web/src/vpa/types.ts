import type {Ref} from '../api/types'
export interface VPAResources {cpu?:string;memory?:string}
export interface VPADeclaration {container:string;requests:VPAResources;limits:VPAResources}
export interface VPARecommendation {container:string;target:VPAResources;lowerBound:VPAResources;upperBound:VPAResources;uncappedTarget:VPAResources}
export interface VPAPolicy {container:string;mode:string;controlledValues:string;controlledResourcesKnown:boolean;controlledResources:string[];minAllowed:VPAResources;maxAllowed:VPAResources}
export interface VPACondition {type:string;status:string;transitionTime?:string;freshness:string}
export interface VPASource {ref:Ref;resourceVersion:string;apiVersion:string;recommender?:string;updateMode:string;state:string;freshness:string;partial:boolean;conditions:VPACondition[];recommendations:VPARecommendation[];policies:VPAPolicy[]}
export interface VPAHPAMetric {ref:Ref;resourceVersion:string;resource:string;container?:string;targetType:string}
export interface VPAInspection {ref:Ref;resourceVersion:string;capturedAt:number;state:string;hpaState:string;sources:VPASource[];declarations:VPADeclaration[];hpaMetrics:VPAHPAMetric[]}
