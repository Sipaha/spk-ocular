# SPK Ocular 1.6.1

- Linux-DEB/RPM-Pakete benötigen glibc 2.35 oder neuer statt 2.39. Unter Ubuntu 22.04 und Debian 12 lassen sich die Pakete regulär installieren, wenn GTK 3 und WebKit2GTK 4.1 verfügbar sind.
- Beide Linux-Architekturen durchlaufen vor der Veröffentlichung native Installations-, Browsermodus- und GTK-Fenstertests unter Ubuntu 22.04. Die Paketprüfung weist außerdem Programme zurück, die glibc-Symbole oberhalb der angegebenen Mindestversion benötigen.
- Das Archiv zur nativen Verifikation ergänzt die bisherigen sechs Plattformen um zwei Ubuntu-22.04-Berichte und Screenshots. Diese Prüfungen betreffen X11 und den Userspace mit dem Kernel des Testsystems, nicht sämtliche Kernel, GPU-Treiber oder Wayland-Sitzungen.
- Konfigurationsbestätigungen fokussieren zuverlässig Abbrechen und halten Tastatureingaben im Dialog. Enter auf Abbrechen schließt die Prüfung, ohne verschlüsselte Konfigurationen zu löschen.
- Im Menü Add kubeconfig aktivieren Enter und die Leertaste den fokussierten Eintrag, ohne Tastenkürzel der Zielliste auszulösen. Gedrückt gehaltenes Enter und deaktivierte Einträge bleiben gesperrt.
