import type {Ref} from '../api/types'
export type CapacityQuantity={state:string;value?:string}
export type CapacityResources={cpu:CapacityQuantity;memory:CapacityQuantity}
export type NodeCapacityRequest={ref:Ref;namespace:string}
export type NodeCapacity={ref:Ref;namespace:string;resourceVersion:string;capturedAt:number;allocatable:CapacityResources;requests:CapacityResources;coverage:string;excludedTerminal:number;unverified:number;pods:{ref:Ref;resourceVersion:string;requests:CapacityResources}[]}
