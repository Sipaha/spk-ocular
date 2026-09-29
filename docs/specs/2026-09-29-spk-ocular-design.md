# SPK Ocular — лёгкий локальный просмотрщик инфраструктуры

Дата: 2026-09-29. Статус: план утверждён пользователем 2026-09-29; P0 (каркас), P1 (ресурсы,
детали, метрики) и P2 (логи) реализованы и проверены на kind 2026-09-29 (`docs/plans/`);
следующий — P3 (exec и port-forward).
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

Удобство важнее экономии: экономия, замедляющая UI или ухудшающая пользовательский опыт, не
принимается (как в spk-mm-client; подтверждено пользователем 2026-09-29: «сильно не упарываться
оптимизациями памяти, которые ухудшат UX»). Оптимизация памяти оправдана, только если её цена
для пользователя незаметна (пример — slim-кэш: +~0,1 с на 10k объектов при первой загрузке).
Считается только собственная память (Private_Dirty). Память, которая делится с другими
процессами (общие библиотеки WebKit/GTK/ICU, доля в PSS), нас почти не волнует и
оптимизациями не преследуется (решение пользователя 2026-09-29).

## Не делаем в MVP

CI/CD, GitOps, создание кластеров, Helm UI, monitoring/alerting-платформа, централизованное
хранение логов, collaboration, свой cloud backend, редактирование/apply YAML,
read-only/protected-пометка contexts, трей, несколько активных кластеров одновременно.

## Kubernetes MVP — функции

- Contexts из kubeconfig сразу при старте: `KUBECONFIG` с merge first-wins (иначе
  `~/.kube/config`) — как kubectl, плюс остальные kubeconfig-файлы прямо в `~/.kube` (как
  Lens/outwall: у пользователя там лежат kubeconfig-и, не подключённые через `KUBECONFIG`);
  id стабилен: `kubeconfig:<имя>` / `file:<путь>:<имя>` (доп. файлы показывают имя файла). Живое обновление при изменении
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

Архитектура подтверждена независимым ревью (сессия Codex, 2026-09-29); контракт ниже
уточнён по его замечаниям — без тяжёлого журнала событий и ACK/replay.

**События.** Шина `internal/events` — не поток происшествий, а инвалидации: у каждого
подписчика почтовый ящик «последнее событие на (type, key)» + wake на одно место; `Emit`
не блокирует и не теряет последнее состояние, переполнение ящика (> 1024 ключей)
схлопывается в одно `resync`. SSE (пере)открытие — тоже `resync`. Реализовано в P0.

**Вид (view)** — открытый запрос таблицы: `OpenView(target, query)` → `{viewId, kind,
columns}`; `CloseView(viewId)`. Запрос вида неизменяем (сменили namespace/kind/фильтр
сервера — новый вид). `viewId` непрозрачный и никогда не переиспользуется (эпоха процесса +
счётчик), поэтому курсор старого вида не может «оказаться новее» нового.

**Дельты.** Informer → hot-layer вида: текущие строки + журнал «последнее изменение на id»
и надгробия удалённых, ограниченный по числу записей; версия — счётчик Ocular (не
`resourceVersion` Kubernetes). `Coalescer` 100 мс → `view_changed {viewId, version}` (ключ —
viewId). UI: `GetRows(viewId, since)` → `{viewId, version, reset, upserts, deleted,
status}`; `since=0` или курсор вне сохранённого диапазона → `reset` с полным снимком
(пустой снимок — тоже валидный ответ). Данные ответа и его версия снимаются атомарно,
строки — неизменяемые снимки. Клиент двигает курсор только после применения ответа (не
по версии из события), держит один запрос на вид и тянет снова, пока применённая версия
меньше объявленной; ошибка `GetRows` — ограниченные повторы, пока вид открыт; ответы
закрытого/старого вида игнорируются. Тот же name с другим UID — удаление + добавление
(client-go может сообщить это как Update): состояние, привязанное к объекту, сбрасывается.

**Статус вида** — часть ответа и повод для `view_changed` даже без изменения строк:
`loading` → `ready` (граница начального снимка) → `stale` (переподключение: последние
данные остаются, но помечены) / `error` с классом (`forbidden`, `unavailable`, `gone`,
`unsupported`, …). Запрет никогда не показывается пустой таблицей. Если список namespaces
запрещён, а конкретный namespace читается, — namespace можно ввести вручную (и default из
kubeconfig). Жизнь watch привязана к аренде вида, а не к контексту HTTP-запроса или
Wails-вызова (там `context.Background`); у каждой операции свой таймаут/отмена.

**Сессия target-а** строится из снимка его конфигурации; изменилась конфигурация context
(сервер/аутентификация — видно по хешу разрешённого конфига) — сессия и её виды
пересоздаются. Действия выполняются по явной паре (сессия, Ref), а не через «текущий
выбор».

### Потоки

Логи (и в P3 — exec) идут через loopback HTTP-сервер с токеном, а не через `wails://`:
WebKitGTK падает на fetch с `Blob`-телом через `wails://`, WebView2 буферизует стримы (опыт
spk-mm-client и SPK-launcher). Реализовано в P2 (`internal/streams`):

- Поток открывается в два шага: API `OpenLogStream(ref, query)` проверяет запрос и регистрирует
  его под одноразовым id, привязанным к инкарнации сессии (TTL подключения 30 с, работа — только
  после подключения); страница делает `GET <StreamBase>/logs/<id>`. Закрытие сессии завершает её
  потоки кадром `end{gone}` (терминально: «Открыть заново» начинает поток с чистого буфера); reaper сессий
  считает открытые потоки использованием.
- Desktop: `http://127.0.0.1:<случайный порт>/<токен>` (ленивый старт, точная проверка `Host`,
  Origin страницы — ровно `wails://localhost`, ACAO только ему); browser-режим: тот же
  обработчик под `/streams/<токен>/` основного сервера.
- Кадры NDJSON (`source`, `lines`, `state`, `ready`, `ping`, `end`), коалесцирующий сброс
  (50 мс / 32 КБ), дедлайн записи 60 с (не читающая страница отключается), «толчок» `ping`
  через 100 мс после сброса и heartbeat 20 с: WebKitGTK иногда придерживает хвост пачки, пока
  не придут новые байты (спайк `docs/spikes/2026-09-29-log-stream-desktop.md`).
- Лимиты: ≤ 8 потоков на приложение, ≤ 64 одновременных запросов `pods/log`, ≤ 20
  потоков-контейнеров на вкладку.

### Provider API

Сигнатуры реализованы в P0–P2 (`internal/provider`, `internal/core`); ниже — суть.

```go
// Идентичность объекта. Kind квалифицирован (k8s: "apps/deployments", "pods"), чтобы CRD
// разных групп не сталкивались; GVR остаётся внутри адаптера.
type Ref struct {
    Provider, Target, Scope, Kind, Name, UID string
}

// Scope-селектор явный: все / один / неприменимо — не через "".
type ScopeSel struct { Mode ScopeMode; Name string } // ScopeAll | ScopeOne | ScopeNone

type Health struct {                 // сводка = проблема с наивысшим приоритетом
    State  HealthState               // ok|progressing|warning|error|terminating|unknown
    Reason, Message string
    Issues []Issue
}

type Row struct {                    // неизменяемая проекция для таблиц
    ID, Rev string                   // ID = UID (замена = delete+add); Rev = resourceVersion
    Ref    Ref
    Cells  []Cell
    Health Health
}

type Provider interface { ID() string; Title() string; Discover(ctx) (Discovery, error) }
type Opener   interface { Open(ctx, target string) (Session, error) } // без сети

type Session interface {
    ConfigHash() string
    Kinds() []KindDescriptor          // KindDescriptor.Logs — у объектов kind-а есть логи
    Scopes(ctx) ([]Scope, error)
    ScopeKind() string                // kind, чьи строки — scopes (живой список)
    // Watch: синхронный неблокирующий Sink; начальное состояние завершается
    // Status{Ready}, дальше изменения; сбои — статусы stale/error с классом.
    Watch(q Query, sink Sink) (stop func(), err error)
    Get(ctx, ref Ref) (*Resource, error) // полный объект; UID сверяется
    Close()
}

// Опциональные возможности — type assertion.
type MetricsSource interface { Metrics(ctx, q Query) (Metrics, error) }
type LogSource interface {
    LogInfo(ctx, ref Ref) (LogInfo, error)   // каналы (контейнеры), default, агрегат, previous
    // StreamLogs пишет в LogSink (Source/Lines/State/Ready; может блокировать —
    // обратное давление) до ctx или конца; проблемы источников — состояния.
    StreamLogs(ctx, ref Ref, q LogQuery, sink LogSink) error
}
// Впереди (P3/P4): Execer, PortForwarder, Actioner (дескрипторы действий на объект).
```

Структурированные ошибки API: `forbidden`, `unavailable`, `gone`, `conflict`,
`unsupported`, `not_found`, `bad_request`, `internal`. Общая часть — «список объектов с
состоянием, детали, связи, возможности». Pods, namespaces и containers не проникают в
`core`. UI рисует любую сущность через `KindDescriptor` + `Row` + `Resource`; для
отдельных kind провайдер регистрирует свою панель (Pod — контейнеры с Logs/Exec), иначе —
общая (поля + YAML + events + связи). (`core.Detail` из P0 — пара «ключ/значение» факта,
полный объект называется `Resource`.)

### Проверка абстракции на Docker Compose

| Понятие | Kubernetes | Docker Compose |
|---|---|---|
| Target | kube context | Docker context/host |
| Scope | namespace | compose project (label `com.docker.compose.project`) |
| Kinds | pods, deployments, … | services (наблюдаемые), containers, networks, volumes, images |
| Health | phase, conditions, restarts | state + healthcheck |
| Relations | deployment → rs → pods | service → containers; networks/volumes/images — many-to-many |
| Events | Events API | `docker events` — подсказка + пересверка списком |
| Logs / Exec | ✓ | ✓ |
| PortForward | ✓ | нет — порты уже опубликованы; capability не реализуется |
| Metrics | metrics.k8s.io | streaming stats API |
| Actions | restart, scale, delete | restart, stop/start, rm; scale/create — только при известной compose-модели |

Уточнение после ревью (2026-09-29): наблюдения Docker Engine — не желаемая модель Compose.
По labels видны только существующие контейнеры: сервис без контейнеров не обнаружить,
желаемое число реплик неизвестно. Поэтому сервис — синтетическая сущность с устойчивым id
(endpoint/project/service), контейнеры — её воплощения; «желаемое» показывается как
неизвестное, пока нет compose-файла; scale/create включаются только при доступной
compose-модели. Images и внешние networks/volumes не принадлежат проекту — связи
many-to-many. `docker events` хранит лишь последние 256 событий: поток событий — подсказка,
источник правды — список + пересверка изменённых id (после переподключения — полный
relist); медленные inspect — в ограниченном пуле с поколениями на id. Это ложится на тот же
контракт Watch (snapshot/ready/upsert/delete/status) без семантики `resourceVersion`.

## Ключевые технические решения

1. **Ленивые informers с бюджетом.** Ключ кэша — поколение сессии + GVR + нормализованный
   namespace/селектор (кластерные ресурсы — без namespace). Поднимаются при открытии вида
   (или Problems/связей — они тоже берут аренду), держатся ~60 с после последней аренды,
   но число/объём неактивных кэшей ограничен (LRU), при смене context старые кэши
   освобождаются сразу. Остановленный informer не перезапускается — только пересоздаётся.
   Resync — 0; обработчики только дешёвая неизменяемая проекция, без сети и блокирующих
   отправок. Более широкий уже открытый кэш переиспользуется; ради экономии не заводится
   watch на весь кластер для пользователя с правами на один namespace.
2. **Компактные списочные кэши.** Dynamic client + transform с белым списком полей на kind:
   идентичность, UID, resourceVersion, namespace, labels, ownerReferences и то, что нужно
   health/связям/колонкам; вырезаются `managedFields`, last-applied, крупный spec; значения
   Secret/ConfigMap в списочных кэшах не хранятся. Transform идемпотентен, обрабатывает
   `DeletedFinalStateUnknown`; hot-layer не мутирует объекты кэша. Полный объект — отдельный
   `GET` с проверкой UID. Профилирование P1 показало, что у trimmed Unstructured основная
   цена — накладные расходы `map[string]any`, поэтому в кэше лежит `slimObject`: типизированный
   `ObjectMeta` (ключи, владельцы, удаление) + JSON отфильтрованного тела; проекция
   разворачивает его один раз на изменение. Синтетика (10k «реальных» pods): без фильтра
   25,3 КБ/pod, trimmed Unstructured 6,1 КБ, slim 1,3 КБ; развернуть и спроецировать все
   10k — ~110 мс.
3. **Health считает backend (provider).** UI не знает, что такое CrashLoopBackOff. Правила:
   Succeeded — успешное завершение, не «не готов»; Ready=false на старте — Progressing с
   grace; PodScheduled=False/Unschedulable, ошибки образа/конфигурации — конкретные
   причины; deletionTimestamp — Terminating, не Failed; lastState OOMKilled — недавняя
   история, если текущий сбой не продолжается; workloads — observedGeneration vs
   generation, desired=0, прогресс/дедлайн rollout, правила StatefulSet/DaemonSet; Node
   Ready=Unknown ≠ подтверждённый сбой; Warning events — подкрепляющие недавние
   свидетельства (dedup, lastSeen, count), а не вечные проблемы.
4. **Время без опроса.** Правила «Pending дольше N», «недавний рестарт» истекают без
   событий API — для этого один локальный планировщик дедлайнов (переоценка объекта в
   нужный момент, отмена при update/delete/выселении), тесты на фейковых часах. Это
   локальный таймер, а не опрос кластера — принцип «никаких фоновых опросов» не нарушен.
5. **Рост рестартов — история наблюдений.** Первое наблюдение pod (UID + контейнер) задаёт
   базу (100 старых рестартов — не «100 недавних»); рост — положительная дельта +
   локальное время в ограниченном кольце в памяти (без SQLite); уменьшение счётчика или
   новый UID — новая база; разрывы наблюдения помечаются, чтобы не утверждать точное время.
6. **Problems — «проблемы в наблюдаемой области».** Открытый вид Problems берёт аренды
   минимальных детекторов (pods, workloads, nodes, events) для выбранной области и честно
   показывает покрытие; эти аренды входят в тот же бюджет. Связи (Deployment → ReplicaSet →
   Pod) тоже берут нужные внутренние наблюдения (ReplicaSet), а не зависят от того, какие
   таблицы пользователь открывал.
7. **Один активный context.** Выбор target-а закрывает сессии остальных; сессия без видов
   закрывается через 60 с (страница ушла, вид «осиротел»); UI переключает выбор сразу,
   не дожидаясь ответа, — иначе старая страница успевала переоткрыть вид на закрытой
   сессии (найдено замером). Список scopes — живой вид kind, названного провайдером
   (`ScopesView.Kind`, k8s: namespaces).
8. **Действия.** restart — patch аннотации `kubectl.kubernetes.io/restartedAt`; scale — через
   subresource `scale`; delete — подтверждение с явным context/namespace/именем.
9. **Никаких фоновых опросов в ядре.** Фича, требующая постоянного опроса кластера, в ядро не
   попадает; метрики опрашиваются только для видимой таблицы.

## Состояние (SQLite)

`~/.spk/ocular/ocular.db`, `modernc.org/sqlite`, WAL, одно соединение, встроенные миграции
`internal/store/migrations/NNNN_*.sql`, многошаговые записи только через `Store.WithTx`
(паттерн spk-mm-client). Таблицы: `ui_prefs` (выбранный target — `selected_target`) и
`target_state` (provider, target, key → JSON: последний `kind` и `scope` — с P1; ширины и
порядок колонок — позже). Недавние переходы для палитры — P5.
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
- Интеграция: реальный кластер в kind (`make kind-up`, `make test-kind`, `make e2e-kind`);
  фикстуры `scripts/kind-seed.sh` (функциональные), `kind-rbac.sh` (пользователи с правами на
  один namespace и без watch), `kind-metrics.sh` (metrics-server), `kind-load.sh` (нагрузка);
  замер — `tests/e2e/measure-kind.mjs`. Цели kind падают без кластера, `make check` герметичен.
- e2e: Playwright против `--browser` с изолированным `SPK_OCULAR_HOME` и `--test-api`.
- Реальный кластер через outwall — только с разрешения пользователя и только чтение.
- Единый гейт `make check`: gofmt, vet, golangci-lint, `go test -race`, eslint, tsc, vitest,
  проверка бандла, build.

## Этапы

- **P0** ✅ — каркас: Go + Wails + web, `API` с двумя транспортами, store/paths, `core`/`provider`,
  список contexts из kubeconfig с живым обновлением, browser-режим, `make check`. Замер
  (desktop, Xvfb, 3 contexts): Private_Dirty 96 МБ (Go 55 + WebProcess 34 + NetworkProcess 7),
  окно ~0,2 с; бинари 14 МБ (browser) / 22 МБ (desktop); стартовый чанк 79 КБ gz.
- **P1** ✅ — 11 видов (+ скрытый ReplicaSets) с живыми таблицами и health, namespaces (живой
  список, ручной ввод при запрете), детали (факты, YAML, связи с переходами, события объекта),
  CPU/RAM из metrics.k8s.io, состояние по target-у. Проверено на kind: права только на
  namespace, list без watch, metrics-server, живое масштабирование. Замеры (kind: 3k pending
  pods + 5k ConfigMaps, из них 500 по ~60 КБ): холодная таблица 3k pods 0,6–1,5 с, 5k
  ConfigMaps 1,8–4 с, тёплая 0,2–1 с; backend Private_Dirty 20 МБ с pods, 28 МБ с pods+
  ConfigMaps, после 13 быстрых переходов 30 МБ (2 активных + 8 неактивных кэшей), после смены
  context и простоя — 11 МБ; desktop с открытой таблицей 3k pods — 148 МБ Private_Dirty всех
  процессов (Go 53, WebProcess 95, Network 7), окно 0,21 с.
- **P2** ✅ — логи: pod (контейнер/все, previous, since, tail), агрегат workload-а (живой набор
  pods по UID контроллера, merge backlog-а по времени, ≤ 20 потоков-контейнеров), переживает
  рестарты контейнера (курсор на инкарнацию, пропуск повтора по счёту строк) и rollout; вьюер —
  порт SPK-launcher (виртуализация, follow, выделение) + ANSI, уровни, поиск (regex в Worker
  с бюджетом времени), префиксы источников, сохранение; нижняя панель вкладок. Проверено на
  kind (рестарт без дублей, scale up/down, RBAC без `pods/log` и без watch pods) и в desktop
  под Xvfb: 50 000 строк — 143 МБ Private_Dirty всех процессов, 5 циклов открыть/закрыть —
  плато ~140 МБ (Go ~41, WebProcess ~90).
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
