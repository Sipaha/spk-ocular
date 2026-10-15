# SPK Ocular 1.7.0

- Les listes Kubernetes chargent intégralement le type demandé et les namespaces sélectionnés grâce à des caches compacts partagés, de petites pages automatiques et des watches. Un budget de lecture par session et une rétention limitée des caches inutilisés réduisent les appels API superflus et la mémoire conservée.
- Les lectures All Namespaces trop volumineuses s’arrêtent et demandent de choisir des namespaces précis, sans présenter une liste partielle comme complète.
- Les icônes d’actualisation uniformes relisent les listes et tournent jusqu’à la fin. Le chargement affiche le temps écoulé et le volume de données d’inventaire reçu, lorsqu’il est disponible. Annuler remplace la liste par un message persistant ; Réessayer ou une nouvelle connexion relance le chargement.
- Le graphe du cluster ne se charge qu’après un clic explicite pour chaque connexion. La sélection, la recherche et l’ouverture des détails conservent la caméra.
- La sélection des connexions, les libellés de types, les infobulles, la navigation de la palette de commandes dans le périmètre choisi et la sélection des logs ont été améliorées. Le nouvel affichage des ports d’une ressource ne répète plus la même demande d’information.
- Les mesures locales d’inventaire et de mémoire, les données brutes, la méthode et les limites figurent dans `docs/performance.md` ; elles ne garantissent pas les performances de tous les clusters de production.
