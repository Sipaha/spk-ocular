# SPK Ocular 1.9.0

- Ressourcenlisten, Helm, Graph, Timeline, Security und RBAC verwenden einheitliche Werkzeugleisten sowie Laden mit erhaltenen Inhalten, Fortschritt, Abbruch und Wiederholung. Helm steht links nach Storage.
- Pod-Eigenschaften trennen reguläre, Init- und Debug-Container, behalten Sidecar-Markierungen und umbrechen lange Namen und Images.
- Kalenderdaten verwenden in allen Sprachen Ortszeit, das 24-Stunden-Format und dd.mm.yyyy; der ursprüngliche ISO-Zeitstempel erscheint beim Darüberfahren.
- Deployments lassen sich direkt in den Eigenschaften skalieren. Die Aktionsprüfung zeigt den Verbindungsnamen; Minus/Plus ändern die Replikazahl innerhalb der Grenzen vor der Prüfung.
- Die Auswahl einer Vergleichsbasis verschiebt den Arbeitsbereich nicht mehr. Textunterschiede enthalten Kontext und begrenzte Seitennavigation.
- Neue Container-Exec-Berechtigungen gelten für den gewählten Namespace beziehungsweise das Projekt ohne Ressourcentypauswahl. Bestehende eingeschränkte Freigaben und Bestätigungen bleiben erhalten; Checkbox-Zustände sind deutlicher.
- Logs zeigen Verbindung und Ressource, verwenden den gesamten Zeitraum und ordnen Filter vor der lokalen Suche an. Die Backend-Filterung durchsucht begrenzte aktuelle Historie schrittweise; Treffer erscheinen während des Ladens, neue Live-Zeilen werden parallel erfasst. Grenzen und unvollständige Ergebnisse bleiben sichtbar.
- Downloads öffnet sich automatisch und speichert ohne Nachfrage im System-Downloadordner. Ein Rechtsklick auf den Log-Download wählt einen anderen Ort. Doppelte Namen erhalten einen Zusatz; aktive Aufträge lassen sich auch für separate Log-Fenster abbrechen.
- Ein Klick auf eine abgeschlossene Download-Zeile öffnet die Datei. Erfolgreiches Öffnen oder Anzeigen im Ordner schließt das Popup. Position, Zeilenhöhe und Log-Fußzeile bleiben bei Fortschrittsänderungen stabil.
- Der Verbindungsfortschritt verschiebt Symbol und Titel nicht mehr. YAML öffnet ohne unformatiertes Zwischenbild, lädt den Editor bei Nutzungsabsicht vor und verwendet ihn beim Ressourcenwechsel weiter; Entwurfsschutz bleibt erhalten.
