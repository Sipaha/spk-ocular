# SPK Ocular 1.6.2

- Beim Ändern der Höhe des Protokollbereichs behalten sichtbare Zeilen ihre Bildschirmposition. Follow bleibt bei der neuesten Zeile; das Lesen älterer Einträge aktiviert Follow nicht erneut. Verkleinern, Vergrößern, Abbrechen der Größenänderung und Wiederanzeigen eines ausgeblendeten Protokolltabs erhalten die Position innerhalb des verfügbaren Scrollbereichs.
- Das Ein- und Ausschalten von Wrap hält bei aktivem Follow die neueste Zeile sichtbar, auch wenn Wrap während einer Größenänderung umgeschaltet wird.
- Neue Konfiguration entsperrt den lokalen verschlüsselten Speicher direkt mit dem Master-Passwort und öffnet anschließend den YAML-Editor, ohne Connect zu verlangen oder eine Clusterverbindung aufzubauen. Das funktioniert auch, wenn bereits ein Master-Passwort existiert, aber noch keine verschlüsselte Konfiguration gespeichert oder die letzte gelöscht wurde.
- Das Abbrechen der Passworteingabe verändert den Speicher nicht. Connect entsperrt weiterhin und setzt ausschließlich die ausdrücklich gewählte Verbindung fort. Die Erklärung zum Erstellen einer Konfiguration ist in allen acht Anwendungssprachen verfügbar.
