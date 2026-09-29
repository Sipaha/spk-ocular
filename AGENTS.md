# spk-ocular — гид для агентов

Лёгкий локальный просмотрщик инфраструктуры: «Lens-like visibility + kubectl-like простота,
без тяжеловесности Lens». Первый provider — Kubernetes, следующий — Docker Compose.
Go + Wails v3 + React. Спецификация: `docs/specs/2026-09-29-spk-ocular-design.md`.
Планы: `docs/plans/`. Бэклог: `docs/backlog.md`.

Статус: P0 (каркас) готов — `docs/plans/2026-09-29-p0-skeleton.md`. Следующий — P1.

## Сборка и тесты

- `make build` — web + бинарь browser-режима `build/bin/spk-ocular` (CGO off).
- `make build-desktop` — desktop `build/bin/spk-ocular-desktop` (теги `wails gtk3`, CGO); собирается
  во временный файл и атомарно переносится (`mv`). `make release` — то же с тегом `production`
  (без DevTools).
- `make run` — `build-desktop` + запуск. `make run-browser` — UI на http://127.0.0.1:5190 (`PORT=`)
  с настоящим kubeconfig и `~/.spk/ocular`.
- `make test` = `test-go` (`go test -race`) + `test-web` (vitest) + `test-e2e` (Playwright против
  browser-режима с фикстурным `KUBECONFIG`/`HOME`/`SPK_OCULAR_HOME` в `tests/e2e/.run/`).
- `make lint` — go vet и golangci-lint (с тегами desktop и без) + eslint + tsc.
- **`make check`** — гейт перед каждым коммитом: lint, все тесты, обе сборки.
- `make pss PID=<pid>` — Private_Dirty/PSS процесса и его WebKit-детей (бюджет ~150 МБ Private_Dirty).
- `E2E_BIN=<путь> E2E_PORT=<порт>` — e2e против другой browser-сборки.
- Проверка desktop без экрана пользователя: `xvfb-run -a -s "-screen 0 1400x900x24" <скрипт>`,
  окно ищется `xwininfo -root -tree | grep '"SPK Ocular"'`, снимок — `import -window root`.

## Устройство (коротко)

- `internal/api` — интерфейс `API` + DTO; `transport/http.go` (browser: `POST /api/<Method>`, SSE
  `/api/events`, bearer из `<meta name="spk-ocular-api-token">`), `transport/wails.go` (бинды,
  FQN `github.com/spk/spk-ocular/internal/api/transport.API.<Method>`). Новый метод API = метод в
  интерфейсе + `Service` + маршрут в `http.go` + метод в `wails.go` + `web/src/api/client.ts`.
- `internal/events` — неблокирующий `Emitter` + `Coalescer` (порт spk-mm-client).
- `internal/core` — provider-агностичные типы; `internal/provider` — `Provider`, опциональные
  интерфейсы (`TargetWatcher`), `Registry`.
- `internal/providers/kubernetes` — contexts из kubeconfig (`kubeconfig.go`), inotify-watch
  (`watch.go`).
- `internal/store` — SQLite, миграции `migrations/NNNN_*.sql`, `ui_prefs`, `target_state`.
- `internal/desktop` — Wails-окно, D-Bus probe, GPU policy. `cmd/spk-ocular` — cobra, режимы.
- `web/` — React/Vite/Tailwind/zustand; `tests/e2e/` — Playwright.

## Правила (правило — причина — тест)

- Kubernetes — provider, а не модель приложения: в `internal/core` и общем UI нет pod/namespace/
  container. — Абстракцию проверяет Docker Compose (см. спецификацию).
- Никаких фоновых процессов и постоянных опросов; изменения kubeconfig — только inotify. Закрытие
  окна завершает приложение (трея нет). — `TestWatchSeesAtomicRenameWriteOnce`.
- Credentials не покидают kubeconfig/Go: нет в БД, DTO, логах, событиях. —
  `TestTargetsNeverCarryCredentials`, e2e «lists contexts…» (`SECRET` не на странице).
- `Discover` читает только локальные файлы, без сети: он на пути старта. — старт окна ~0,2 с.
- Источники kubeconfig: `KUBECONFIG` (иначе `~/.kube/config`) с merge first-wins как у kubectl,
  плюс остальные kubeconfig-файлы прямо в `~/.kube` (не-kubeconfig молча пропускаются). Битый
  основной файл — `Problem`, остальные contexts видны. — `internal/providers/kubernetes/kubeconfig_test.go`.
- Id target-а стабилен и не зависит от остальной конфигурации: `kubeconfig:<имя>` для contexts
  kubectl, `file:<путь>:<имя>` для доп. файлов; показывается `Title` (имя), у доп. файлов в
  подзаголовке — имя файла. Иначе запомненный выбор молча «переезжал» бы на другой кластер. —
  `TestExtraContextIDIsStableWhenPrimaryGainsSameName`.
- Запомненный выбор target не стирается, пока target временно пропал из kubeconfig: вернётся —
  выбор тоже. — `TestSelectionIsRememberedAndHiddenWhileTargetIsAbsent`, e2e.
- Browser-режим отвечает только на loopback-`Host` (защита от DNS rebinding: `/` отдаёт токен). —
  `TestNonLoopbackHostIsRejected`.
- `/api/_test/*` — только с `--test-api` и токеном. — `TestTestAPIOnlyWithFlagAndToken`.
- Destructive-действия — только с подтверждением, в котором виден context/namespace/объект.
- Версии `github.com/wailsapp/wails/v3` и `@wailsio/runtime` совпадают (сейчас `3.0.0-beta.26`).
- `go build ./...` без тега `wails` обязан проходить: desktop-код за тегом.
- Стартовый JS-чанк < 300 КБ gz, xterm/CodeMirror — только ленивые чанки. — `web/scripts/check-bundle.mjs`
  (часть `pnpm build`).
- Wails `LogLevel` — Warn: Info логирует каждый asset-запрос, Debug — результаты биндингов.
- Горячие клавиши — по `KeyboardEvent.code` (`web/src/keyboard.ts`), иначе не работают в русской
  раскладке.
- Каталог данных — `~/.spk/ocular` (`SPK_OCULAR_HOME`); временные файлы агентов — в
  `.agents/tmp` solution, не в `/tmp`.

## Things that bite

- **kubeconfig — YAML 1.1**: голые `y`/`n`/`on`/`off`/`yes`/`no` — булевы, context с таким именем
  без кавычек роняет разбор всего файла (`cannot unmarshal bool into … name of type string`). В
  фикстурах имена всегда в кавычках.
- **Playwright выполняет `playwright.config.ts` в раннере И в каждом воркере**: `mkdtemp` в конфиге
  без защиты даёт воркерам другой каталог, чем у webServer. Каталог создаётся один раз и передаётся
  через `process.env.E2E_ROOT` (воркеры наследуют env).
- **Язык UI**: gettext-порядок `LANGUAGE` > `LC_ALL` > `LC_MESSAGES` > `LANG`; у пользователя
  `LANGUAGE=en_US`, поэтому `LANG=ru_RU…` один не даёт русский UI. `LC_ALL=C` — английский.
- **jsdom** не знает `scrollIntoView` — заглушка в `web/vitest.setup.ts`; `@wailsio/runtime` там же
  замокан (побочные эффекты при импорте).
- **Мёртвая/зависшая D-Bus**: GLib подключается без таймаута и окно не появляется. Probe 2 с и
  подмена `DBUS_SESSION_BUS_ADDRESS` (`internal/desktop/busprobe_linux.go`); проверено: окно за
  0,2 с при недоступной шине.
- Кандидаты из соседей, ещё не встреченные здесь (spk-mm-client AGENTS.md): fetch с `Blob`/
  `FormData`-телом через `wails://` роняет WebKitGTK; скрытое окно WebKitGTK копит rAF — потоки
  (логи, exec) пойдут через loopback-сервер с токеном, не через `wails://`.
