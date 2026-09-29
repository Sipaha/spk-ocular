# Бэклог (после MVP)

- **Docker Compose provider** — следующий provider; проверка абстракции (см. спецификацию).
- Docker provider, SSH provider (`~/.ssh/config`), кастомные provider-ы.
- Интеграция с агентами Claude/Codex (поэтому состояние сразу в SQLite — решение пользователя
  2026-09-29).
- Несколько активных кластеров одновременно / несколько окон.
- CRD-виды (generic-колонки, `additionalPrinterColumns`).
- macOS и Windows сборки и упаковка (nfpm deb/rpm, AppImage).
- Редактирование/apply YAML.
