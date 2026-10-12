# SPK Ocular 1.4.0

## Recherche et outils rapides

- Ctrl+K récupère les requêtes QWERTY anglaises saisies avec une disposition russe ou ukrainienne compatible lorsque la requête initiale ne donne aucun résultat. Le texte, les périmètres et l’identité de la ressource sont conservés ; un message traduit montre la requête récupérée.
- L’ouverture d’un objet depuis la recherche donne le focus à sa ligne sélectionnée. Les journaux, le terminal et les fichiers d’un Pod précis utilisent le conteneur par défaut en cours d’exécution, sinon le premier conteneur en cours d’exécution. Les journaux restent disponibles pour les Pods terminés. Les réplicas conservent un choix explicite du Pod ; le clic droit ouvre la configuration complète.
- Ouvrir ou rouvrir les journaux donne le focus à leur zone. Follow indique End dans l’infobulle et l’aide clavier ; les champs de texte gardent le comportement habituel d’End.

## Interface, fiabilité et performances

- Les fenêtres modales recouvrent les barres de défilement et séparateurs d’arrière-plan. Le focus du tableau inclut l’en-tête et les lignes ; les menus restent au-dessus du panneau inférieur et les infobulles près des boutons.
- Les dialogues de lecture et de configuration partagent le comportement du focus, de Tab et de fermeture. La protection des modifications non enregistrées est conservée.
- Le journal de l’agent rejette les réponses obsolètes après un changement de filtre, conserve les cibles historiques et formate les dates selon la langue. Les relations, révisions de Deployment et ressources Helm utilisent des tableaux compacts et alignés.
- Les requêtes HTTP de l’interface ont des limites de taille et de durée de lecture. Les contrôles d’identité de session et l’annulation des requêtes d’inventaire en attente empêchent les anciennes lectures de basculer vers une session de remplacement. Droits, périmètres et budgets des fournisseurs restent inchangés.
- La recherche regex dans les journaux traite des lots incrémentaux sans conserver une seconde histoire dans le worker. Les projections Compose utilisent des index locaux ; les lignes inchangées et mises à jour du seul statut évitent le travail redondant.
- Les huit langues, l’exécutable commun bureau/navigateur et la vérification obligatoire des six variantes de plateforme sont conservés.
