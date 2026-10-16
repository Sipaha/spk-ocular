# SPK Ocular 1.7.1

- Correction du défilement de toute la page et de l’espace vide sous la barre d’état lorsque la fenêtre est réduite avec un grand panneau de journaux. Le panneau s’adapte désormais à la hauteur disponible et les barres supérieure et inférieure restent en place.
- Les compilations locales avec Make incluent désormais l’heure de compilation UTC à la seconde et une révision Git courte dans la version affichée en bas de fenêtre, dans la boîte À propos et par la commande version. Les numéros des versions publiées restent inchangés.
- Correction d’une condition de concurrence lors de l’annulation de Helm upgrade : le nettoyage de l’opération pouvait marquer comme échouée une mise à niveau déjà réussie. L’annulation active et les délais restent appliqués.
