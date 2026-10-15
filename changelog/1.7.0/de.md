# SPK Ocular 1.7.0

- Kubernetes-Ressourcenlisten laden den angeforderten Typ und die ausgewählten Namespaces vollständig über kompakte gemeinsame Caches, automatische kleine Seiten und Watches. Ein gemeinsames Lesebudget pro Sitzung und begrenzte Aufbewahrung ungenutzter Caches verringern unnötige API-Zugriffe und Speicherbelegung.
- Zu große Abfragen über All Namespaces werden mit der Aufforderung zur Auswahl konkreter Namespaces angehalten. Teillisten werden nicht als vollständig dargestellt.
- Einheitliche Aktualisierungssymbole lesen Listen neu ein und drehen sich bis zum Abschluss. Beim Laden werden die verstrichene Zeit und, sofern verfügbar, die empfangenen Inventardaten angezeigt. Abbrechen ersetzt die Liste durch einen dauerhaften Hinweis; Erneut versuchen oder eine neue Verbindung startet den Ladevorgang wieder.
- Der Clustergraph wird pro Verbindung erst nach einem ausdrücklichen Klick geladen. Auswahl, Suche und Detailansicht behalten die Kameraposition bei.
- Verbindungsauswahl, Ressourcentypen, Tabellenhinweise, bereichsbezogene Suche in der Befehlspalette und Logauswahl wurden verbessert. Das erneute Rendern der Ports einer ausgewählten Ressource wiederholt nicht mehr dieselbe Informationsabfrage.
- Lokale Inventar- und Speichermessungen mit Rohdaten, Methodik und Einschränkungen stehen in `docs/performance.md`. Sie sind keine allgemeine Leistungsgarantie für Produktionscluster.
