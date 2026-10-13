# SPK Ocular 1.6.0

## Container-Befehle für Agenten

- Lokale Agenten entdecken Instanzen und bereiten endliche Befehle über `GetExecInfo`, `PrepareExec` und `RunExec` vor. Getrennte stdout/stderr-Ausgaben werden als base64 mit beobachtetem Exit-Status sowie Ausgabe- und Zeitlimits zurückgegeben.
- Die separate Berechtigung `exec` erfordert benannte Ressourcentypen und eine explizite Instanz-/Container-Auswahl. Standardmäßig wird jeder Befehl mit genauem argv, endlicher stdin-Eingabe, Ziel und Limits bestätigt. Keine dauerhafte interaktive Shell, implizite Shell, attach oder automatische Wiederholung.
- Kubernetes-Namespace- und Docker-Compose-Projektgrenzen, Zielidentität, UID und Konfigurationsrevision bleiben geprüft. Berechtigungsänderungen brechen den Transport ab; das Ende des entfernten Prozesses ist nur best effort. Unvollständige oder unbekannte Ergebnisse bleiben sichtbar. Eigenständige Docker-Container erben keine Projektberechtigungen.
- Exec nutzt Container-Rechte und kann eingebundene Geheimnisse lesen oder Container-/Hostdaten ändern. Befehle, Eingaben und Ausgaben werden nicht im Auditjournal gespeichert. Bestehende Rechte erhalten exec nicht automatisch.

## Korrekturen

- Die Löschbestätigung für Downloads fokussiert nach der Vorbereitung zuverlässig Abbrechen.
- Endliche Docker-Eingaben senden EOF ohne stdout/stderr zu schließen; Befehle ohne Eingabe lösen stdin ab. Nicht unterstützte Transporte werden vor dem Start abgelehnt.
- Deutsche Agentenbestätigungen zeigen korrekt kodierten Text. Neue exec-Einstellungen und Bestätigungen sind in allen acht Oberflächensprachen übersetzt.

Pakete behalten das gemeinsame Desktop-/Browser-Programm und Prüfungen auf allen sechs nativen OS-/Architektur-Kombinationen.
