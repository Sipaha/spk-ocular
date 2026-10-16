# SPK Ocular 1.8.0

- Einzelne ConfigMap-Werte lassen sich mit Syntaxhervorhebung, Suche und Rückgängig bearbeiten. Die Vorschau zeigt die genaue Textänderung gegenüber dem aktuellen Wert; Binärdaten und Secret-Inhalte behalten ihre geschützte Darstellung.
- Die Ressourcensuche unterstützt Fragmente von Namespace, Typ und Name. Bei mehrdeutigen Namespaces erscheint eine Auswahl; ein eindeutiger aktueller Treffer öffnet das genaue Objekt unter Wahrung ungespeicherter Änderungen.
- Navigationsgruppen haben einheitliche Symbole und Abstände. Beim Einklappen werden alle Untereinträge einschließlich der ausgewählten Ressource ausgeblendet; die geöffnete Seite bleibt erhalten.
- Nicht verbundene Arbeitsbereiche zeigen eine statische Anbieterübersicht, Connect/Cancel beim Status und separate transparente Leuchtturm- und Bergillustrationen. Der gezeichnete Leuchtturm mit weichem Lichtstrahl behält seine Größe und wird beim Ändern der Seitenleistenbreite beschnitten.
- Eine gemeinsame Fußzeile zeigt den Status der aktiven Ansicht. Aktualisierungsdetails erscheinen nach zwei Sekunden ohne Verschieben der Bedienelemente; Fokus- und Größenänderungen verschachtelter Bereiche verschieben nicht den Anwendungsrahmen.
- Alt+Shift+Pfeiltasten ändern die Größe des fokussierten Bereichs. Alt+Shift+Enter löst das Logfenster oder führt es zurück, ohne einen zweiten Stream zu öffnen; neue Fenster nutzen mehr von der Arbeitsfläche des aktuellen Bildschirms.
- Dockmenüs schließen alle Tabs oder die Tabs links beziehungsweise rechts und berücksichtigen angeheftete Tabs und ihre Zugehörigkeit.
- Ressourcendetails verwenden besser lesbare Typografie und zusammenpassende kompakte Revisions- und Events-Tabellen. Events behält Sortierung, Spaltenbreitenänderung und Refresh; Ingress-Servicebeziehungen zeigen jeden deklarierten Routenpfad.
- Bereits übertragener Terminaltext wird bei einer IME-Ersetzung nicht erneut gesendet. Aktive Texteingabekomposition, Unicode-Einfügen und Screenreader-Ansagepuffer bleiben geschützt.
