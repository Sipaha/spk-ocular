# SPK Ocular 1.4.0

## Suche und Schnellzugriff

- Ctrl+K stellt englische QWERTY-Abfragen wieder her, die mit russischer oder kompatibler ukrainischer Tastaturbelegung eingegeben wurden, wenn die ursprüngliche Abfrage keine Treffer hat. Eingabe, Bereiche und Ressourcenidentität bleiben erhalten; ein lokalisierter Hinweis zeigt die korrigierte Abfrage.
- Nach dem Öffnen eines Suchtreffers erhält die ausgewählte Tabellenzeile den Fokus. Logs, Terminal und Dateien eines konkreten Pod verwenden den laufenden Standardcontainer, sonst den ersten laufenden Container. Logs bleiben für abgeschlossene Pods verfügbar. Bei mehreren Workload-Replikaten bleibt die explizite Pod-Auswahl erhalten; Rechtsklick öffnet die vollständige Einrichtung.
- Das Öffnen oder erneute Öffnen von Logs fokussiert den Logbereich. Follow zeigt End im Tooltip und in der Tastaturhilfe; Textfelder behalten das übliche End-Verhalten.

## Oberfläche, Zuverlässigkeit und Leistung

- Modale Fenster verdecken Hintergrund-Scrollleisten und Panel-Trennlinien. Der Tabellenfokus umfasst Kopfzeile und Zeilen; Menüs bleiben über dem unteren Panel und Tooltips neben ihren Schaltflächen.
- Lese- und Einrichtungsdialoge verwenden einheitliches Fokus-, Tab- und Schließverhalten. Der Schutz ungespeicherter Änderungen bleibt bestehen.
- Das Agentenjournal verwirft veraltete Antworten nach Filterwechseln, behält historische Ziele und formatiert Daten in der gewählten Sprache. Ressourcenbeziehungen, Deployment-Revisionen und Helm-Ressourcen verwenden kompakte, ausgerichtete Tabellen.
- HTTP-Anfragen der Oberfläche haben begrenzte Körpergrößen und Lesefristen. Sitzungsidentitätsprüfungen und die Abbruchbehandlung wartender Inventarabfragen verhindern veraltete Zugriffe auf Ersatzsitzungen. Berechtigungen, Bereiche und Provider-Budgets bleiben unverändert.
- Die Regex-Logsuche verarbeitet neue Zeilen stapelweise ohne zweite Historie im Worker. Compose-Projektionen verwenden lokale Indizes; unveränderte Zeilen und reine Statusaktualisierungen vermeiden unnötige Arbeit.
- Alle acht Oberflächensprachen, das gemeinsame Desktop-/Browser-Programm und die verpflichtende Prüfung der sechs Plattformvarianten bleiben erhalten.
