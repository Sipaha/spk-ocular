# SPK Ocular 1.9.0

- Ressources, Helm, Graph, Timeline, Security et RBAC partagent des barres et des contrôles cohérents de chargement, progression, annulation et reprise sans effacer le contenu. Helm figure à gauche après Storage.
- Les propriétés Pod séparent les conteneurs ordinaires, init et debug, conservent les étiquettes Sidecar et adaptent les noms et images longs.
- Les dates utilisent l’heure locale, le format 24 heures et dd.mm.yyyy dans toutes les langues ; le survol révèle l’horodatage ISO original.
- Redimensionnez les Deployments depuis leurs propriétés. La vérification affiche le nom de connexion ; les boutons moins/plus ajustent les réplicas dans les limites autorisées avant la revue.
- Choisir une référence de comparaison ne décale plus l’espace de travail. Les différences textuelles incluent du contexte et une pagination bornée.
- Les nouveaux droits container-exec couvrent le namespace/projet choisi sans sélecteur de types. Les droits restreints et confirmations existants sont conservés ; les cases actives et désactivées sont mieux différenciées.
- Les logs identifient connexion et ressource, utilisent toute la période et placent le filtre avant la recherche locale. Le backend explore progressivement un historique récent borné ; les résultats partiels apparaissent pendant le chargement tandis que les nouvelles lignes sont capturées en parallèle. Limites et résultats incomplets restent explicites.
- Downloads s’ouvre automatiquement et enregistre dans le dossier de téléchargements du système sans question. Un clic droit sur le téléchargement des logs choisit la destination. Les doublons reçoivent un suffixe ; les tâches actives peuvent être annulées, même depuis une fenêtre de logs séparée.
- Cliquez sur une ligne terminée pour ouvrir le fichier. L’ouverture réussie du fichier ou de son dossier ferme le panneau. Position, hauteur des lignes et pied des logs restent stables pendant la progression.
- La progression de connexion ne déplace plus l’icône et le titre. YAML s’ouvre sans affichage intermédiaire en texte brut, précharge l’éditeur à l’intention d’ouverture et réutilise son instance au changement de ressource ; les brouillons restent protégés.
