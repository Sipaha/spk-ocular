# SPK Ocular — лёгкий локальный просмотрщик инфраструктуры

Дата: 2026-09-29. Статус: план утверждён пользователем 2026-09-29; P0 (каркас) реализован и
проверен 2026-09-29 (`docs/plans/2026-09-29-p0-skeleton.md`), следующий — P1.
Решения приняты пользователем в переписке; документ фиксирует итог, а не варианты.

## Зачем

Lens даёт хорошую видимость Kubernetes, но тяжёлый (Electron, сотни МБ памяти,
расширения, облачные аккаунты). kubectl/k9s лёгкие, но без обзорности.

Формула продукта: **Lens-like visibility + kubectl-like простота, без тяжеловесности Lens.**
MVP — быстрый operational viewer для Kubernetes: понять, что запущено и что сломалось,
и быстро дойти от проблемного workload до pod → container → logs/events.

## Принципы

- **Local-first.** Никакого backend/cloud-сервиса Ocular; credentials остаются локально.
- **Lightweight.** Быстрый старт, мало памяти, ноль фоновых процессов (закрыл окно — всё остановлено).
- **Существующая инфраструктура.** kubeconfig для Kubernetes, Docker contexts для Docker,
  `~/.ssh/config` для SSH. Своей системы credentials нет.
- **Provider architecture.** Kubernetes — первый provider, а не модель приложения. Общий UI
  работает через абстракцию; provider-specific возможности допустимы. Docker не изображается
  как Kubernetes.
- **Read-first.** Главный сценарий — чтение. Destructive-действия требуют подтверждения.
  Все contexts, включая прод, работают в RW-режиме; отдельной read-only/protected-пометки
  в MVP нет (решение пользователя 2026-09-29).

## Критерии успеха

| Метрика | Цель |
|---|---|
| Запуск → окно со списком contexts | < 1 с (kubeconfig читается без сети) |
| Первая таблица pods после выбора context | < 1 с + сетевой RTT |
| Память — Private_Dirty всех процессов (Go + WebKit) | ориентир ~150 МБ; **обязательно — без роста за час работы** (замер `scripts/pss.sh`) |
| Размер бинаря | < 50 МБ; стартовый JS-чанк < 300 КБ gz, xterm/CodeMirror — только ленивые чанки |
| Фоновые процессы | 0 |

Удобство важнее экономии: экономия, замедляющая UI, не принимается (как в spk-mm-client).

## Не делаем в MVP

CI/CD, GitOps, создание кластеров, Helm UI, monitoring/alerting-платформа, централизованное
хранение логов, collaboration, свой cloud backend, редактирование/apply YAML,
read-only/protected-пометка contexts, трей, несколько активных кластеров одновременно.

## Kubernetes MVP — функции

- Contexts из kubeconfig сразу при старте: `KUBECONFIG` с merge first-wins (иначе
  `~/.kube/config`) — как kubectl, плюс остальные kubeconfig-файлы прямо в `~/.kube` (как
  Lens/outwall: у пользователя там лежат kubeconfig-и, не подключённые через `KUBECONFIG`);
  дубликат имени из доп. файла получает id `<имя> (<файл>)`. Живое обновление при изменении
  файлов (inotify). Сделано в P0. Переключение context и namespace
  (включая «все namespaces»).
- Виды: Pods, Deployments, StatefulSets, DaemonSets, Services, Ingresses, ConfigMaps, Secrets,
  Nodes, Events. Живое обновление через watch.
- Состояние и проблемы с первого взгляда: health каждой строки + сводный вид **Problems**.
- Details: поля, YAML (read-only, CodeMirror 6), связанные Events, связанные объекты
  (owner вверх, pods вниз, service → pods).
- Логи: stream/follow, tail, выбор контейнера, previous, since, агрегация всех pod-ов workload
  в один поток с префиксом, поиск/фильтр, ANSI-цвета.
- Exec в контейнер (xterm.js, TTY, resize).
- Port-forward: список активных туннелей, все закрываются при выходе.
- Действия: restart (как `kubectl rollout restart`), scale, delete (+ delete pod для пересоздания).
  Подтверждение с явным context/namespace/объектом.
- Метрики CPU/RAM для pods и nodes через `metrics.k8s.io` — если API есть, только для видимой
  таблицы, опрос ~15 с (решение пользователя 2026-09-29: в MVP).
- Навигация: `Ctrl+K` — палитра с fuzzy-поиском по объектам и видам; команды в стиле k9s
  (`:pods`, `:deploy`); `/` — фильтр таблицы; хлебные крошки drill-down; полная клавиатурная
  навигация. Горячие клавиши по `KeyboardEvent.code` — работают в русской раскладке.

## Стек

- **Go 1.26**, модуль `github.com/spk/spk-ocular` (как spk-mm-client).
- **client-go**: `clientcmd` (kubeconfig, exec-плагины, OIDC — ровно как kubectl), `dynamic` +
  informers, `remotecommand` (exec по WebSocket с фолбэком на SPDY), `portforward`,
  `metrics` client. Пакеты `k8s.io/kubectl` — где выгодно переиспользовать логику (выбор pod-ов
  workload для логов, restart).
- **Wails v3 `v3.0.0-beta.26`** (последняя на 2026-09-29; spk-mm-client на beta.25), версии
  Go-модуля и `@wailsio/runtime` совпадают. Сравнение с Rust + Tauri 2 проведено 2026-09-29, выбран Go (решение пользователя):
  client-go и пакеты kubectl — главный аргумент; память определяет webview, он у обоих одинаков.
- **Frontend**: React 19, Vite 8, TypeScript 6, Tailwind 4 (`@theme`-токены), zustand 5,
  `@tanstack/react-virtual`, CodeMirror 6 (лениво), xterm.js (лениво), pnpm, vitest, Playwright.
- **SQLite** через `modernc.org/sqlite` (pure Go) — состояние приложения (решение пользователя
  2026-09-29: нормальная БД сразу, продукт будет развиваться, в перспективе — интеграция с
  агентами Claude/Codex).
- Платформа MVP — Linux; macOS/Windows позже (код не должен их исключать).

## Имена и расположение

- Бинари: `spk-ocular` (browser-режим/CLI), desktop-сборка с тегами `wails gtk3`.
  Команды: `spk-ocular` (desktop), `spk-ocular --browser --port N`, `spk-ocular version`.
- Каталог данных: `~/.spk/ocular` (переопределение — `SPK_OCULAR_HOME`), каталог 0700,
  файлы 0600. Внутри: `ocular.db` (SQLite), `tmp/` (решение пользователя 2026-09-29).
- Browser-режим по умолчанию на порту 5190.
- Переменные окружения — префикс `SPK_OCULAR_`.

## Архитектура

```
┌──────────────── UI (React) ─────────────────┐
│ generic: ResourceTable · Detail · Logs · Term│  ← KindDescriptor / Row / Detail
│ provider-specific панели (опционально)       │
└──────▲───────────────────────────▲───────────┘
       │ API: вызовы + события      │ потоки: логи (chunked fetch), exec (WebSocket)
       │ desktop: Wails bindings    │ loopback-сервер с токеном (оба режима)
       │ browser: POST /api + SSE   │
┌──────┴───────────────────────────┴───────────┐
│ api     — интерфейс API, DTO, два транспорта │
│ views   — hot-layer: версии строк, дельты    │
│ events  — неблокирующий Emitter + Coalescer  │
│ streams — логи, exec, port-forward           │
│ store   — SQLite (состояние, настройки)      │
│ core    — Ref, Row, Health, Kind, Caps       │
│ provider registry                            │
├──────────────┬────────────────┬──────────────┤
│ kubernetes   │ dockercompose  │ docker, ssh, │
│ (MVP)        │ (следующий)    │ custom       │
└──────────────┴────────────────┴──────────────┘
```

### Один API — два транспорта (модель spk-mm-client)

- `internal/api` — Go-интерфейс `API` и DTO, независимые от транспорта.
- Desktop: тонкий Wails-сервис поверх `API`, фронт вызывает `Call.ByName(...)`; события —
  `app.Event.Emit`. Бинды пишутся руками, `wails3 generate bindings` не используется.
- Browser (`--browser --port N`): `POST /api/<Method>` с JSON, SSE `/api/events`; per-run токен
  в `<meta>`, `OriginGuard` + `LoopbackHostGuard` (только loopback-`Host`, защита от DNS rebinding).
- Фронт: один `Client` с `wailsClient`/`httpClient`, выбор по `location.protocol === 'wails:'`.
- Browser-режим — основа e2e (Playwright) и быстрой итерации; `--test-api` монтирует
  `/api/_test/*` для тестов.

### Живые таблицы: push-инвалидация, pull-дельта

Informer → hot-layer в Go (строки с монотонной версией на вид) → `Coalescer` (100 мс,
latest-wins) → событие `view_changed {viewId, version}` → UI вызывает
`GetRows(viewId, sinceVersion)` и получает только изменённые строки и id удалённых.
Если `sinceVersion` слишком старая — полный снимок. UI тянет данные, когда готов, —
отдельный backpressure не нужен; большие списки не пересылаются целиком.

### Потоки

Логи и exec идут через loopback HTTP-сервер с токеном, а не через `wails://`: WebKitGTK
падает на fetch с `Blob`-телом через `wails://`, WebView2 буферизует стримы (опыт
spk-mm-client и SPK-launcher). Логи — chunked fetch, exec — WebSocket. В browser-режиме
это тот же сервер.

### Provider API

```go
// Идентичность любого объекта в любом provider-е.
type Ref struct {
    Provider, Target, Scope, Kind, Name, UID string // k8s: context/ns/pods/x; compose: dockerctx/project/services/web
}

type Health struct { State HealthState; Reason, Message string } // OK|Progressing|Warning|Error|Unknown

type Row struct {            // компактная проекция для таблиц; полный объект — только по запросу
    Ref    Ref
    Cells  []any             // по колонкам KindDescriptor
    Health Health
    Owners []Ref             // навигация вверх (pod → rs → deployment)
}

type KindDescriptor struct {
    ID, Title, Group string  // "pods", "Pods", "Workloads"
    Columns []Column         // name, type (age|status|number|text), width hint
    Scoped  bool             // есть ли scope (namespace / compose project)
}

type Provider interface {
    ID() string
    Discover(ctx context.Context) ([]Target, error)   // kube contexts / docker contexts / ssh hosts
    Open(ctx context.Context, target string) (Session, error)
}

type Session interface {
    Kinds() []KindDescriptor
    Scopes(ctx context.Context) ([]Scope, error)       // namespaces / compose projects
    Watch(ctx context.Context, q Query) (<-chan Delta, error)
    Get(ctx context.Context, ref Ref) (*Detail, error) // полный объект, YAML, поля
    Related(ctx context.Context, ref Ref) ([]Ref, error)
    Close() error
}

// Опциональные возможности — type assertion; UI показывает только то, что есть.
type EventSource   interface { Events(ctx context.Context, ref Ref) (<-chan Event, error) }
type LogSource     interface { Logs(ctx context.Context, t LogTarget, o LogOpts) (io.ReadCloser, error) }
type Execer        interface { Exec(ctx context.Context, t ExecTarget, tty TTY) error }
type PortForwarder interface { Forward(ctx context.Context, t ForwardTarget, local int) (Forward, error) }
type MetricsSource interface { Metrics(ctx context.Context, q Query) (map[string]Usage, error) }
type Actioner      interface {
    Actions(ref Ref) []ActionDescriptor // id, title, params, Destructive
    Do(ctx context.Context, ref Ref, action string, params map[string]any) error
}
```

Общая часть — «список объектов с состоянием, детали, связи, возможности». Pods, namespaces
и containers не проникают в `core`. UI рисует любую сущность через `KindDescriptor` + `Row` +
`Detail`; для отдельных kind провайдер может зарегистрировать свою детальную панель
(Pod — контейнеры с кнопками Logs/Exec), иначе — общая панель (поля + YAML + events + related).

### Проверка абстракции на Docker Compose

| Понятие | Kubernetes | Docker Compose |
|---|---|---|
| Target | kube context | Docker context/host |
| Scope | namespace | compose project (label `com.docker.compose.project`) |
| Kinds | pods, deployments, … | services, containers, networks, volumes, images |
| Health | phase, conditions, restarts | state + healthcheck |
| Related | deployment → rs → pods | service → containers |
| Events | Events API | `docker events` с фильтром |
| Logs / Exec | ✓ | ✓ |
| PortForward | ✓ | нет — порты уже опубликованы; capability не реализуется |
| Metrics | metrics.k8s.io | streaming stats API |
| Actions | restart, scale, delete | restart, stop/start, rm, scale service |

Ни одно поле `core` не лишнее и ничего не пришлось изображать Kubernetes-ом.

## Ключевые технические решения

1. **Ленивые informers.** Поднимаются при открытии вида (ключ — context, namespace, kind),
   останавливаются через ~60 с после ухода с вида. `SetTransform` вырезает `managedFields` и
   крупные поля; полный объект — `GET` при открытии деталей.
2. **Dynamic client + проекции.** Меньше зависимостей; CRD позже почти бесплатно (generic-колонки
   или `additionalPrinterColumns`). Typed-пакеты — только для logs/exec/port-forward.
3. **Health считает backend (provider).** UI не знает, что такое CrashLoopBackOff. Детекторы
   Problems: CrashLoopBackOff, ImagePullBackOff/ErrImagePull, OOMKilled, Pending дольше порога,
   рост рестартов, not ready, unavailable replicas, node NotReady/pressure, Warning events.
4. **Один активный context.** Сессии других contexts закрываются после grace-периода.
5. **Действия.** restart — patch аннотации `kubectl.kubernetes.io/restartedAt`; scale — через
   subresource `scale`; delete — подтверждение с явным context/namespace/именем.
6. **Никаких фоновых опросов в ядре.** Фича, требующая постоянного опроса кластера, в ядро не
   попадает; метрики опрашиваются только для видимой таблицы.

## Состояние (SQLite)

`~/.spk/ocular/ocular.db`, `modernc.org/sqlite`, WAL, одно соединение, встроенные миграции
`internal/store/migrations/NNNN_*.sql`, многошаговые записи только через `Store.WithTx`
(паттерн spk-mm-client). Таблицы: `ui_prefs` (выбранный target — `selected_target`) и
`target_state` (provider, target, key → JSON: последний scope и вид, ширины/порядок колонок —
с P1). Недавние переходы для палитры — P5.
Секреты в БД не хранятся — credentials остаются в kubeconfig.

## Лёгкость — как не превратиться в Lens

- Системный webview (Wails), без Chromium и без Node-рантайма.
- Ленивые watch с refcount и grace; strip объектов в informers; дельты вместо снимков.
- Виртуальные таблицы и лог-вьюер; кольцевой буфер логов (50k строк).
- xterm и CodeMirror — ленивые чанки; скрипт сборки падает, если они попали в стартовый чанк.
- `GOGC=50` + `SetMemoryLimit` (как `gomem.go` spk-mm-client), если не заданы в окружении.
- Нет трея, нет фоновых процессов, нет встроенных kubectl/helm/prometheus, нет системы
  расширений, нет телеметрии и аккаунтов.
- Soak-режим с «шумящим» фейковым кластером и замер `scripts/pss.sh` на каждом этапе.

## Переиспользование соседей

- **spk-mm-client**: `internal/api` + transport (Wails/HTTP/SSE, guards), `internal/events`
  (Emitter, Coalescer), `internal/desktop` (D-Bus probe, single-instance, devtools-теги),
  `paths`, `store`, `gomem.go`, `scripts/pss.sh`, Makefile, `.golangci.yml`, vitest-мок Wails,
  `keyboard.ts`, `Splitter`, иконки, i18n ru/en, `useFrames`.
- **SPK-launcher**: пайплайн логов (`useLogStream`, `useLogFilter`, `LogViewport`,
  `logSelection`, `LogViewer`), UI-примитивы (`Modal`/`ConfirmModal`, `ContextMenu`, `Toast`,
  `BottomPanel`, `RightDrawer`, `CodeEditor`, `StatusBadge`), палитра Darcula/Lens;
  для Compose — определение Docker endpoint и `stats.go`. Код launcher — пользователя,
  переносится как есть (решение 2026-09-29).
- Пишем с нуля: Kubernetes-provider, терминал xterm.js, port-forward, палитра команд,
  Docker events watcher (для Compose).

## Тестирование

- Go: fake/dynamic fake client-go для unit; проекции, health и детекторы Problems — таблицы
  фикстур. `go test -race`.
- Интеграция: реальный кластер в kind (Docker есть); бинарь kind — в `.agents/tools` solution.
- e2e: Playwright против `--browser` с изолированным `SPK_OCULAR_HOME` и `--test-api`.
- Реальный кластер через outwall — только с разрешения пользователя и только чтение.
- Единый гейт `make check`: gofmt, vet, golangci-lint, `go test -race`, eslint, tsc, vitest,
  проверка бандла, build.

## Этапы

- **P0** ✅ — каркас: Go + Wails + web, `API` с двумя транспортами, store/paths, `core`/`provider`,
  список contexts из kubeconfig с живым обновлением, browser-режим, `make check`. Замер
  (desktop, Xvfb, 3 contexts): Private_Dirty 96 МБ (Go 55 + WebProcess 34 + NetworkProcess 7),
  окно ~0,2 с; бинари 14 МБ (browser) / 22 МБ (desktop); стартовый чанк 79 КБ gz.
- **P1** — namespaces, 10 видов с watch, health, details/YAML/events/related, метрики.
- **P2** — логи.
- **P3** — exec и port-forward.
- **P4** — действия restart/scale/delete с подтверждениями.
- **P5** — Problems, палитра, клавиатурная навигация, полировка, soak-замер памяти.
- **Затем** — Docker Compose provider.

## Документация и процесс

- Только `AGENTS.md` в корне (без `CLAUDE.md`-симлинка — решение пользователя 2026-09-29):
  «Сборка и тесты», «Правила (правило — причина — тест)», «Things that bite».
- `docs/specs/`, `docs/plans/YYYY-MM-DD-<этап>.md`, `docs/spikes/`, `docs/research/`,
  `docs/backlog.md`. Решения фиксируются в документах с датой, отдельных ADR нет.
- Коммиты: `area: lowercase summary`, без `Co-Authored-By`, без `--amend`.

## Решённые вопросы

- Лицензия — Apache 2.0, как у соседей spk-* (решение пользователя 2026-09-29).
- Код SPK-launcher принадлежит пользователю — переносится как есть (2026-09-29).
- Репозиторий целиком в ведении агента: коммитить свободно, **историю не переписывать**
  (без amend/rebase/force-push) — решение пользователя 2026-09-29.
