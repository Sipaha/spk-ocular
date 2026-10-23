# SPK Ocular 1.10.0

- Connectez des agents locaux au serveur MCP intégré, avec autorisations explicites et opérations examinées. Codex et Claude Code se configurent sur demande ; une configuration manuelle est proposée aux autres clients. MCP Streamable HTTP remplace l’ancien protocole local ; les clients existants doivent être reconfigurés.
- Consultez les vues natives Argo CD et Flux, l’état des applications/contrôleurs et les liens de ressources. Les revues de modification et de mise à l’échelle affichent le contexte GitOps disponible.
- Examinez les workloads, chemins réseau Service/Ingress et releases Helm par des lectures diagnostiques explicites et bornées, avec sources et lacunes visibles.
- Ajoutez des colonnes de labels et enregistrez des vues de tableaux. Consultez l’état et le renouvellement des Certificate cert-manager sans lire le contenu des Secret.
- Lisez un historique Prometheus explicitement sélectionné et les recommandations VPA existantes ; vérifiez la configuration et la fiabilité des workloads dans leur contexte.
- Inspectez les rapports Trivy de configuration/RBAC et de politiques existants par namespace. Les rapports à l’échelle du cluster et l’audit d’une contrainte Gatekeeper choisie nécessitent un consentement distinct ; une preuve absente reste inconnue.
- Importez des rapports KRR dans un stockage temporaire côté backend ou examinez un Job Kubernetes temporaire avant de lancer KRR. Les déclarations de ressources par namespace d’un Node sélectionné ne représentent pas une capacité libre mesurée.
- Importez localement des rapports JSON Polaris bornés, sans installer de scanner ni interroger le cluster pendant l’import.
- Composez les logs de workloads explicitement sélectionnés avec gestion des recouvrements, attribution des sources, filtrage et export fini.
- Ouvrez les assistants Node Shell et Files après revue, avec cycle de vie partagé et suivi durable du nettoyage. Files permet suppression et sélection du fichier révélé ; Alt+Shift+Enter détache ou rattache Files/Shell.
- Apprenez Kubernetes grâce à l’aide hors ligne et à une introduction isolée par tâches, avec retours diagnostiques plus clairs et huit langues.
- Le choix des Pods explique la révision du rollout et la disponibilité ; les indications de comparaison sont plus claires. Les redirections conservent leur état de reconnexion au retrait d’anciens upstreams.
- Les versions sont compilées avec Go 1.26.10, incluant le correctif de sécurité de confiance des certificats.
