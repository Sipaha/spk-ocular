# SPK Ocular 1.1.2

- Compilation avec Go 1.26.9 et golang.org/x/net v0.60.0 pour corriger les vulnérabilités HTTP/2 récemment signalées.
- Parcourez les fichiers des conteneurs Kubernetes et Docker : arborescence mise en cache, cibles des liens symboliques, coloration syntaxique et édition UTF-8 protégée jusqu’à 2 MiB. L’application de bureau copie des fichiers ou dossiers entiers via un sélecteur natif de répertoire, sans écraser les noms existants.
- Affichez les journaux dans une fenêtre native séparée en conservant un seul flux. Choisissez l’emplacement des exports en texte brut. Tous les niveaux sont visibles ; les filtres textuels et la recherche restent disponibles.
- Choisissez un Pod pour les workloads à plusieurs instances et un conteneur pour les Pods à plusieurs conteneurs. Le clic droit sur Logs, Terminal ou Files ouvre la configuration complète ; seul Terminal propose une commande. Une option unique s’ouvre directement au clic gauche et reste visible dans la configuration.
- Consultez les révisions conservées d’un Deployment et comparez le YAML du modèle de Pod en lecture seule avec le Deployment actuel ou une autre révision.
- Les raccourcis suivent les touches physiques, quelle que soit la disposition, y compris dans l’éditeur. Correction des caractères cyrilliques répétés, des mises à jour intermédiaires de taille du terminal et du focus restant sur le séparateur.
- Inspecteur plus spacieux, arborescence redimensionnable, nombres d’entrées en cache et lignes stables pour le chargement et les dossiers vides. Les erreurs ne déplacent plus le contenu. Ressources liées en lignes compactes et actions de fenêtre sous forme d’icônes à droite.

Les opérations de fichiers nécessitent un conteneur Linux actif avec sh, readlink pour les liens et tar pour les téléchargements. Les téléchargements natifs n’ont pas la limite de 2 MiB de l’éditeur et nécessitent l’application de bureau. Les entrées d’archive dangereuses et les conflits de noms sont refusés.

Paquets natifs Linux, Windows et macOS pour amd64 et arm64. Le statut de signature/notarisation reste inchangé et les fichiers des versions précédentes ne sont pas réécrits.
