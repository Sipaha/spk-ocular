/** Reviewed inventory: all described API kinds and well-known placed kinds.
 * API identifiers and field paths deliberately remain language-neutral. */
export const concepts = [
 ['pods','Pod','workload','spec.containers,metadata.ownerReferences,status.conditions','apps/replicasets,services','workloads/pods/'],
 ['apps/deployments','Deployment','workload','spec.replicas,spec.selector,spec.template,status.conditions','apps/replicasets,pods','workloads/controllers/deployment/'],
 ['apps/replicasets','ReplicaSet','workload','spec.replicas,spec.selector,spec.template,metadata.ownerReferences','apps/deployments,pods','workloads/controllers/replicaset/'],
 ['apps/statefulsets','StatefulSet','workload','spec.serviceName,spec.replicas,spec.volumeClaimTemplates','pods,persistentvolumeclaims,services','workloads/controllers/statefulset/'],
 ['apps/daemonsets','DaemonSet','workload','spec.selector,spec.template,status.desiredNumberScheduled','pods,nodes','workloads/controllers/daemonset/'],
 ['batch/jobs','Job','workload','spec.completions,spec.parallelism,spec.backoffLimit,status.conditions','pods,batch/cronjobs','workloads/controllers/job/'],
 ['batch/cronjobs','CronJob','workload','spec.schedule,spec.timeZone,spec.concurrencyPolicy,spec.jobTemplate','batch/jobs','workloads/controllers/cron-jobs/'],
 ['replicationcontrollers','ReplicationController','workload','spec.replicas,spec.selector,spec.template','pods,apps/replicasets','workloads/controllers/replicationcontroller/'],
 ['services','Service','network','spec.selector,spec.ports,spec.type','pods,discovery.k8s.io/endpointslices','services-networking/service/'],
 ['endpoints','Endpoints','network','subsets.addresses,subsets.ports','services,discovery.k8s.io/endpointslices','services-networking/service/#endpoints'],
 ['discovery.k8s.io/endpointslices','EndpointSlice','network','metadata.labels,endpoints.conditions,ports','services,pods','services-networking/endpoint-slices/'],
 ['networking.k8s.io/ingresses','Ingress','network','spec.ingressClassName,spec.rules,spec.tls','services,networking.k8s.io/ingressclasses','services-networking/ingress/'],
 ['networking.k8s.io/ingressclasses','IngressClass','network','spec.controller,spec.parameters','networking.k8s.io/ingresses','services-networking/ingress/#ingress-class'],
 ['networking.k8s.io/networkpolicies','NetworkPolicy','policy','spec.podSelector,spec.policyTypes,spec.ingress,spec.egress','pods,namespaces','services-networking/network-policies/'],
 ['configmaps','ConfigMap','config','data,binaryData','pods,secrets','configuration/configmap/'],
 ['secrets','Secret','secret','type,data,stringData','pods,serviceaccounts','configuration/secret/'],
 ['nodes','Node','node','spec.taints,status.conditions,status.allocatable','pods','architecture/nodes/'],
 ['namespaces','Namespace','namespace','metadata.name,status.phase','resourcequotas,limitranges','overview/working-with-objects/namespaces/'],
 ['events','Event','event','reason,message,count,involvedObject,firstTimestamp,lastTimestamp','pods,nodes','cluster-administration/logging/'],
 ['autoscaling/horizontalpodautoscalers','HorizontalPodAutoscaler','capacity','spec.scaleTargetRef,spec.minReplicas,spec.maxReplicas,spec.metrics','apps/deployments,pods','workloads/autoscaling/horizontal-pod-autoscale/'],
 ['resourcequotas','ResourceQuota','capacity','spec.hard,status.used','namespaces,limitranges','policy/resource-quotas/'],
 ['limitranges','LimitRange','capacity','spec.limits','resourcequotas,pods','policy/limit-range/'],
 ['policy/poddisruptionbudgets','PodDisruptionBudget','disruption','spec.selector,spec.minAvailable,spec.maxUnavailable','pods','workloads/pods/disruptions/'],
 ['scheduling.k8s.io/priorityclasses','PriorityClass','capacity','value,globalDefault,preemptionPolicy','pods','scheduling-eviction/pod-priority-preemption/'],
 ['node.k8s.io/runtimeclasses','RuntimeClass','node','handler,scheduling,overhead','pods,nodes','containers/runtime-class/'],
 ['coordination.k8s.io/leases','Lease','lease','spec.holderIdentity,spec.renewTime,spec.leaseDurationSeconds','nodes','architecture/leases/'],
 ['admissionregistration.k8s.io/mutatingwebhookconfigurations','MutatingWebhookConfiguration','admission','webhooks.rules,webhooks.clientConfig,webhooks.failurePolicy','admissionregistration.k8s.io/validatingwebhookconfigurations','../reference/access-authn-authz/extensible-admission-controllers/'],
 ['admissionregistration.k8s.io/validatingwebhookconfigurations','ValidatingWebhookConfiguration','admission','webhooks.rules,webhooks.clientConfig,webhooks.failurePolicy','admissionregistration.k8s.io/mutatingwebhookconfigurations','../reference/access-authn-authz/extensible-admission-controllers/'],
 ['apiextensions.k8s.io/customresourcedefinitions','CustomResourceDefinition','extension','spec.group,spec.names,spec.scope,spec.versions','namespaces','extend-kubernetes/api-extension/custom-resources/'],
 ['persistentvolumeclaims','PersistentVolumeClaim','storage','spec.resources.requests.storage,spec.accessModes,spec.storageClassName,spec.volumeName','persistentvolumes,storage.k8s.io/storageclasses,pods','storage/persistent-volumes/'],
 ['persistentvolumes','PersistentVolume','storage','spec.capacity,spec.claimRef,spec.persistentVolumeReclaimPolicy','persistentvolumeclaims,storage.k8s.io/storageclasses','storage/persistent-volumes/'],
 ['storage.k8s.io/storageclasses','StorageClass','storage','provisioner,parameters,reclaimPolicy,volumeBindingMode','persistentvolumeclaims,persistentvolumes','storage/storage-classes/'],
 ['serviceaccounts','ServiceAccount','access','automountServiceAccountToken,imagePullSecrets','pods,rbac.authorization.k8s.io/rolebindings','security/service-accounts/'],
 ['rbac.authorization.k8s.io/roles','Role','access','rules.apiGroups,rules.resources,rules.verbs','rbac.authorization.k8s.io/rolebindings','../reference/access-authn-authz/rbac/'],
 ['rbac.authorization.k8s.io/rolebindings','RoleBinding','access','roleRef,subjects','rbac.authorization.k8s.io/roles,rbac.authorization.k8s.io/clusterroles,serviceaccounts','../reference/access-authn-authz/rbac/'],
 ['rbac.authorization.k8s.io/clusterroles','ClusterRole','access','rules.apiGroups,rules.resources,rules.verbs,aggregationRule','rbac.authorization.k8s.io/clusterrolebindings,rbac.authorization.k8s.io/rolebindings','../reference/access-authn-authz/rbac/'],
 ['rbac.authorization.k8s.io/clusterrolebindings','ClusterRoleBinding','access','roleRef,subjects','rbac.authorization.k8s.io/clusterroles,serviceaccounts','../reference/access-authn-authz/rbac/'],
] as const
export type ConceptId = typeof concepts[number][0]
export type Family = typeof concepts[number][2]
export function concept(id: string) { return concepts.find(c => c[0] === id) }
export function documentation(id:string) {
 const entry=concept(id)
 return entry ? new URL(entry[5], 'https://kubernetes.io/docs/concepts/').href : 'https://kubernetes.io/docs/reference/kubernetes-api/'
}
