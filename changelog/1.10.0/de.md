# SPK Ocular 1.10.0

- Lokale Agenten verbinden sich über den integrierten MCP-Server mit expliziten Berechtigungen und geprüften Operationen. Codex und Claude Code lassen sich auf Wunsch einrichten; andere Clients erhalten eine manuelle Konfiguration. MCP Streamable HTTP ersetzt das bisherige lokale Agentenprotokoll; vorhandene Clients müssen neu konfiguriert werden.
- Native Argo-CD- und Flux-Ansichten zeigen Anwendungen, Controller und Ressourcenverknüpfungen. Bearbeitungs- und Skalierungsprüfungen zeigen verfügbaren GitOps-Eigentümerkontext.
- Workloads, Service-/Ingress-Netzwerkpfade und Helm-Releases lassen sich mit expliziten, begrenzten Diagnoseabfragen untersuchen; Datenlücken und Quellen bleiben sichtbar.
- Beschriftungsspalten und gespeicherte Tabellenansichten ergänzen Ressourcenlisten. Certificate-Status und Verlängerungsdaten von cert-manager sind ohne Lesen von Secret-Inhalten verfügbar.
- Explizit ausgewählte Prometheus-Metrikverläufe und bestehende VPA-Empfehlungen lesen sowie kontextbezogene Konfigurations- und Zuverlässigkeitsprüfungen durchführen.
- Vorhandene namensraumbezogene Trivy-Konfigurations-/RBAC- und Richtlinienberichte prüfen. Clusterweite Berichte und das Audit einer ausgewählten Gatekeeper-Constraint erfordern jeweils eine separate Zustimmung; fehlende Daten bleiben unbekannt.
- KRR-Berichte in temporären Backend-Speicher importieren oder einen temporären Kubernetes-Job vor dem KRR-Lauf prüfen. Ressourcendeklarationen je Namensraum eines ausgewählten Nodes sind keine gemessene freie Kapazität.
- Begrenzte Offline-Polaris-JSON-Berichte lokal importieren, ohne dabei Scanner zu installieren oder den Cluster abzufragen.
- Logs explizit ausgewählter Workloads mit Überschneidungsbehandlung, Quellenzuordnung, Filter und endlichem Export zusammenstellen.
- Geprüfte Node-Shell- und Files-Helfer teilen ihren Lebenszyklus und melden die Bereinigung dauerhaft. Files unterstützt Löschen und die Auswahl angezeigter Dateien; Alt+Shift+Enter löst Files/Shell ab oder dockt sie wieder an.
- Kubernetes mit Offline-Hilfe und einem isolierten, auf Aufgaben basierenden Einstieg in acht Sprachen lernen; Diagnosefeedback wurde verbessert.
- Pod-Auswahl erklärt Rollout-Revision und Bereitschaft; Vergleichshinweise sind verständlicher. Portweiterleitungen behalten den Wiederverbindungszustand beim Beenden alter Upstreams.
- Release-Builds verwenden Go 1.26.10 einschließlich der Sicherheitskorrektur für Zertifikatsvertrauen.
