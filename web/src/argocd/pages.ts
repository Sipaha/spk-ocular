export const argoPages={dashboard:'ocular.argocd',Application:'ocular.argocd.applications',ApplicationSet:'ocular.argocd.applicationsets',AppProject:'ocular.argocd.projects',Repository:'ocular.argocd.repositories',Cluster:'ocular.argocd.clusters'} as const
export function argoMode(workspace:string){
 switch(workspace){case 'argocd-applications':return 'Application';case 'argocd-applicationsets':return 'ApplicationSet';case 'argocd-projects':return 'AppProject';case 'argocd-repositories':return 'Repository';case 'argocd-clusters':return 'Cluster';default:return 'dashboard'}
}
