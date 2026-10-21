import type {Ref,HealthState} from '../api/types'
export interface NetworkCheck {code:string;state:HealthState;field:string;ref?:Ref}
export interface NetworkEndpoint {slice:Ref;pod?:Ref;owners:Ref[];addresses:string[];port:number;ready:boolean|null;serving:boolean|null;terminating:boolean|null;checks:NetworkCheck[]}
export interface NetworkRoute {route?:{path:string;host?:string;pathType?:string;defaultBackend?:boolean};field:string;service:Ref;mode:string;externalName?:string;selector?:string;portName?:string;port:number;targetPort:string;protocol:string;checks:NetworkCheck[];endpoints:NetworkEndpoint[]}
export interface NetworkPath {ref:Ref;capturedAt:number;discovery:string;routes:NetworkRoute[];coverage:{source:string;state:string}[];truncated:boolean}
