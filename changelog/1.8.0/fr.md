# SPK Ocular 1.8.0

- Modifiez les valeurs individuelles de ConfigMap avec coloration syntaxique, recherche et annulation. La vérification présente la différence exacte avec la valeur actuelle ; les données binaires et le contenu des Secret conservent leur présentation protégée.
- Recherchez des ressources avec des fragments explicites de namespace, de type et de nom. Les namespaces ambigus proposent un choix ; un résultat actuel unique ouvre l’objet exact en protégeant les modifications non enregistrées.
- Les groupes de navigation ont des icônes et des espacements cohérents. Replier un groupe masque tous ses enfants, y compris la ressource sélectionnée, tout en conservant la page ouverte.
- Les espaces déconnectés affichent un aperçu statique du fournisseur, Connect/Cancel près de l’état et des illustrations transparentes séparées de phare et de montagnes. Le phare dessiné et son faisceau doux gardent une taille fixe et sont rognés lorsque la largeur du volet change.
- Un pied de page unique affiche l’état de la vue active. Les détails d’actualisation apparaissent après deux secondes sans déplacer les commandes ; le focus et le redimensionnement des volets imbriqués ne déplacent pas le cadre de l’application.
- Alt+Shift+flèches redimensionne le volet actif. Alt+Shift+Enter détache ou réintègre les journaux en conservant un seul flux ; les nouvelles fenêtres utilisent davantage l’espace de travail de l’écran actuel.
- Les menus du panneau inférieur ferment tous les onglets, ou ceux à gauche ou à droite, en respectant les onglets épinglés et leur appartenance.
- Les détails des ressources utilisent une typographie plus lisible et des tableaux compacts de révisions et Events harmonisés. Events conserve le tri, le redimensionnement des colonnes et Refresh ; les relations Ingress-service affichent chaque chemin déclaré.
- Le texte du terminal déjà transmis n’est plus renvoyé lors d’un remplacement IME. La composition active, le collage Unicode et les tampons d’annonce des lecteurs d’écran restent protégés.
