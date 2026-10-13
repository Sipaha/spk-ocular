# SPK Ocular 1.6.0

## Commandes d’agents dans les conteneurs

- Les agents locaux découvrent les instances et préparent/exécutent des commandes finies via `GetExecInfo`, `PrepareExec` et `RunExec`. stdout/stderr séparés sont renvoyés en base64, avec état de sortie observé et limites de sortie et de durée.
- Le droit distinct `exec` exige des types nommés et le choix explicite de l’instance/conteneur. Chaque commande demande une confirmation par défaut avec argv, stdin fini, cible et limites exacts. Pas de shell interactif permanent, de shell implicite, d’attach ni de répétition automatique.
- Les limites des namespaces Kubernetes et projets Docker Compose, l’identité, l’UID et la révision de configuration restent contrôlés. Les changements de droits annulent le transport ; l’arrêt distant reste best effort. Les résultats incomplets ou inconnus sont explicites. Les conteneurs Docker autonomes n’héritent pas des droits des projets.
- Exec utilise les droits du conteneur et peut lire des secrets montés ou modifier les données du conteneur/hôte. Commandes, entrée et sortie ne sont pas conservées dans le journal d’audit. Les droits existants ne reçoivent pas exec automatiquement.

## Corrections

- La confirmation de suppression d’un téléchargement place le focus sur Annuler après la préparation.
- L’entrée finie Docker transmet EOF en conservant stdout/stderr ; les commandes sans entrée détachent stdin. Les transports incompatibles sont refusés avant le démarrage.
- Les confirmations allemandes affichent un texte correctement encodé. Les contrôles et confirmations exec sont traduits dans les huit langues.

Les paquets conservent l’exécutable bureau/navigateur unifié et la vérification sur les six combinaisons natives de système et d’architecture.
