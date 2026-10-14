# SPK Ocular 1.6.2

- Le redimensionnement du panneau de journaux conserve la position des lignes visibles à l’écran. Follow reste sur la dernière ligne ; consulter l’historique ne réactive pas Follow. Réduire, agrandir, annuler le redimensionnement ou réafficher un onglet masqué conserve la position dans les limites du défilement disponible.
- Activer ou désactiver Wrap garde la dernière ligne visible lorsque Follow est actif, même si Wrap est modifié pendant le redimensionnement.
- Nouvelle configuration déverrouille directement le stockage local chiffré avec le mot de passe maître, puis ouvre l’éditeur YAML sans exiger Connect ni établir de connexion au cluster. Cela fonctionne aussi si un mot de passe maître existe déjà mais qu’aucune configuration chiffrée n’a encore été enregistrée, ou que la dernière a été supprimée.
- Annuler la saisie du mot de passe ne modifie pas le stockage. Connect continue à déverrouiller puis à poursuivre uniquement la connexion explicitement sélectionnée. L’explication pour créer une configuration est disponible dans les huit langues de l’application.
