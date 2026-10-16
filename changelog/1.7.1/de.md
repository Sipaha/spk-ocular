# SPK Ocular 1.7.1

- Das Scrollen der gesamten Seite und der Leerraum unter der Statusleiste bei einem großen Protokollbereich und einem kleineren Fenster wurden behoben. Der Bereich passt sich jetzt an die verfügbare Höhe an; Kopf- und Fußzeile bleiben an ihrem Platz.
- Lokale Make-Builds zeigen jetzt einen UTC-Build-Zeitstempel mit Sekundengenauigkeit und eine kurze Git-Revision in der Versionsangabe der Fußzeile, im Info-Dialog und im Befehl version. Veröffentlichte Versionsnummern bleiben unverändert.
- Eine Race Condition beim Abbruch von Helm upgrade wurde behoben: Das Aufräumen des Operationskontexts konnte ein bereits erfolgreiches Upgrade als fehlgeschlagen markieren. Aktiver Abbruch und Zeitlimits bleiben wirksam.
