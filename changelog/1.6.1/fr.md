# SPK Ocular 1.6.1

- Les paquets Linux DEB/RPM nécessitent glibc 2.35 ou ultérieure au lieu de 2.39. Ils peuvent être installés normalement sous Ubuntu 22.04 et Debian 12 si GTK 3 et WebKit2GTK 4.1 sont disponibles.
- Les deux architectures Linux passent des vérifications natives d’installation, de mode navigateur et de fenêtre GTK sous Ubuntu 22.04 avant publication. Le contrôle des paquets refuse aussi les exécutables exigeant des symboles glibc supérieurs au minimum déclaré.
- L’archive de vérification native ajoute deux rapports et captures Ubuntu 22.04 aux preuves des six plateformes existantes. Ces tests couvrent X11 et l’espace utilisateur avec le noyau du système de test ; ils ne certifient pas tous les noyaux, pilotes GPU ou sessions Wayland.
- Les confirmations de configuration placent correctement le focus sur Annuler et gardent les événements clavier dans le dialogue. Entrée sur Annuler ferme la confirmation sans supprimer les configurations chiffrées.
