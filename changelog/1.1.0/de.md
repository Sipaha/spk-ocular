# SPK Ocular 1.1.0

- Go 1.26.9 und golang.org/x/net v0.60.0 beheben die neu gemeldeten HTTP/2-Schwachstellen.
- Containerdateien in Kubernetes und Docker durchsuchen: zwischengespeicherter Ordnerbaum, Ziele symbolischer Links, Syntaxhervorhebung und geschütztes Bearbeiten von UTF-8-Dateien bis 2 MiB. Die Desktop-App kopiert ganze Dateien oder Ordner über einen nativen Verzeichnisdialog; vorhandene Zielnamen werden nicht überschrieben.
- Protokolle in einem eigenen nativen Fenster anzeigen, ohne einen zweiten Stream zu starten. Speicherort für Klartextexporte auswählen. Alle Protokollstufen bleiben sichtbar; Textfilter und Suche stehen weiterhin zur Verfügung.
- Bei Workloads mit mehreren Pods einen Pod und bei Pods mit mehreren Containern einen Container auswählen. Rechtsklick auf Logs, Terminal oder Files öffnet die vollständige Einrichtung; nur Terminal bietet ein Befehlsfeld. Eine einzelne Option öffnet sich bei Linksklick direkt und bleibt in der Einrichtung sichtbar.
- Gespeicherte Deployment-Revisionen ansehen und schreibgeschütztes Pod-Vorlagen-YAML mit dem aktuellen Deployment oder einer anderen Revision vergleichen.
- Tastenkürzel folgen physischen Tasten unabhängig vom Tastaturlayout, auch im Editor. Wiederholte kyrillische Terminaleingaben, Zwischenmeldungen bei Größenänderungen und verbleibender Fokus auf der Trennlinie wurden korrigiert.
- Mehr Platz im Dateiinspektor, verstellbarer Baum, zwischengespeicherte Eintragszahlen und stabile Lade-/Leerzeilen. Fehler verschieben das Layout nicht. Verknüpfte Ressourcen erhalten kompakte Zeilen; Fensteraktionen stehen als Symbole rechts.

Dateioperationen benötigen einen laufenden Linux-Container mit sh, für symbolische Links readlink und für Downloads tar. Native Downloads unterliegen nicht der Editorgrenze von 2 MiB und erfordern die Desktop-App. Unsichere Archiveinträge und Namenskollisionen werden abgewiesen.

Native Pakete für Linux, Windows und macOS auf amd64 und arm64. Signierungs-/Notarisierungsstatus und ältere Release-Dateien bleiben unverändert.
