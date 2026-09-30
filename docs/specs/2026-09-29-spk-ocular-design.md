# SPK Ocular — лёгкий локальный просмотрщик инфраструктуры

Дата: 2026-09-29. Статус: план утверждён пользователем 2026-09-29; P0 (каркас), P1 (ресурсы,
детали, метрики) и P2 (логи) реализованы и проверены на kind 2026-09-29, P3 (терминалы и
туннели) и P4 (действия) — 2026-09-30 (`docs/plans/`); P5 (Problems, палитра, клавиатура,
полировка) — 2026-09-30 (soak памяти принят на сборке P7); P6 (Docker
Compose: просмотр и логи) и P7 (Compose: терминал, статистика, действия) — 2026-09-30.
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
| Память — Private_Dirty всех процессов (Go + WebKit) | ориентир ~150 МБ; **обязательно — без роста за час работы** (замер `scripts/pss.sh`, критерий soak — ниже) |
| Размер бинаря | < 50 МБ; стартовый JS-чанк < 300 КБ gz, xterm/CodeMirror — только ленивые чанки |
| Фоновые процессы | 0 |

Удобство важнее экономии: экономия, замедляющая UI или ухудшающая пользовательский опыт, не
принимается (как в spk-mm-client; подтверждено пользователем 2026-09-29: «сильно не упарываться
оптимизациями памяти, которые ухудшат UX»). Оптимизация памяти оправдана, только если её цена
для пользователя незаметна (пример — slim-кэш: +~0,1 с на 10k объектов при первой загрузке).
Считается только собственная память (Private_Dirty). Память, которая делится с другими
процессами (общие библиотеки WebKit/GTK/ICU, доля в PSS), нас почти не волнует и
оптимизациями не преследуется (решение пользователя 2026-09-29).

«Без роста за час» проверяется soak-прогоном (сценарий по кругу + churn kind, срез в конце
каждой фазы; `scripts/soak-verdict.py`). Критерий — решение пользователя 2026-09-30:
кратковременные колебания (рост на ~10 МБ с последующим спадом) допустимы; в каждой
одноимённой фазе сценария Private_Dirty остаётся в пределах **+100 МБ** от уровня этой фазы в
первом круге после прогрева (20 мин); после остановки нагрузки память возвращается (в тот же
запас от уровня спокойных фаз) и стоит ровно (≥ 10 мин точек, разброс ≤ 10 МБ, конец − начало
≤ 5 МБ). PID-ы не меняются, счётчики stats ограничены, swap 0.

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
- Exec в контейнер (xterm.js, TTY, resize). Сделано в P3.
- Port-forward: список активных туннелей, все закрываются при выходе. Сделано в P3.
- Действия: restart (как `kubectl rollout restart`), scale, delete (+ delete pod для пересоздания).
  Подтверждение с явным context/namespace/объектом.
- Метрики CPU/RAM для pods и nodes через `metrics.k8s.io` — если API есть, только для видимой
  таблицы, опрос ~15 с (решение пользователя 2026-09-29: в MVP). С P7 спрашиваются только
  **видимые строки** (≤ 100, без overscan), сразу при смене видимого набора (300 мс покоя),
  один запрос в полёте на вид (новый набор ждёт, выигрывает последний), уход/скрытие
  страницы отменяют запрос и в Go (запрос несёт `seq`; в Go на вид один запрос, новый или
  `CancelMetrics` обрывает старый, отменённый до старта не выполняется); сортировка по метрике
  держит порядок до нового выбора сортировки или смены набора строк, неизвестное — последним в
  обоих направлениях; каждая метрика отдельно может быть неизвестна или частична («≥»);
  неудавшийся запрос не оставляет старые значения текущими; значение строки, изменившейся с
  замера (health или значения ячеек, не время), сразу не показывается, вид спрашивается снова
  не раньше 5 с после прошлого запроса (смены за это время склеиваются);
  отсечение > 100 и причина пустых колонок видны заметкой над таблицей.
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
выбор». Терминалы и туннели (P3) сессии не принадлежат: провайдер выдаёт хэндл со своим
снимком соединения, и хэндл переживает сессию; наружу хеш не уходит (он покрывает учётные
данные) — UI получает `configRev` (HMAC с ключом процесса) у target-а и у живого ресурса и
помечает ресурс, открытый до смены конфигурации («config changed»).

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
- Терминалы (P3): тот же сервер, WebSocket `<wsBase>/term/<id>` (`coder/websocket`), id
  одноразовый, вид `term` (≤ 16, владелец — приложение), Origin обязателен и проверяется до
  потребления id. Бинарные кадры — байты терминала, текстовые — JSON: `resize`, `ack{n}`
  (накопительно обработанный вывод; окно 1 МиБ), `state`, `iack{n}` (ввод, записанный в stdin;
  у страницы ≤ 256 КиБ в полёте), `intr` (Ctrl+C: вне окна ввода, сервер сбрасывает очередь
  ввода и пишет `^C` первым), `exit{code}`, `end{reason}`. Читатель сокета не блокируется,
  resize — «последнее значение» (повтор имеющегося размера пропускается), WS-ping 20 с, страница
  без `ack` 60 с отключается. Закрытие вкладки или приложения «вешает трубку» in-band (`^C`, `^D`),
  отмена — через 2 с. Запуски терминала принадлежат ему: закрытая вкладка (`ForgetTerminal`)
  завершает и отзывает их.
- Туннели (P3) — не потоки к странице: `internal/forwards` слушает loopback (127.0.0.1 и тот же
  порт на ::1) и несёт соединения через `Upstream` провайдера; UI получает список
  (`ListForwards`) и событие `forwards_changed` (состояния сразу, счётчики ≤ 1/с). Лимиты:
  ≤ 32 туннелей, ≤ 64 соединений на туннель, ≤ 256 на приложение.

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
// rowIDs — строки, которые страница показывает; Usage{CPU, Memory *float64,
// CPUPartial, MemoryPartial}: nil — неизвестно (не 0), partial — сумма не всех частей.
type MetricsSource interface { Metrics(ctx, q Query, rowIDs []string) (Metrics, error) }
type LogSource interface {
    LogInfo(ctx, ref Ref) (LogInfo, error)   // каналы (контейнеры), default, агрегат, previous
    // StreamLogs пишет в LogSink (Source/Lines/State/Ready; может блокировать —
    // обратное давление) до ctx или конца; проблемы источников — состояния.
    StreamLogs(ctx, ref Ref, q LogQuery, sink LogSink) error
}
// P3 (KindDescriptor.Exec / .Forward): хэндлы не зависят от сессии.
type Execer interface {
    ExecInfo(ctx, ref Ref) (ExecInfo, error)   // экземпляры (pods) и каналы (контейнеры)
    PrepareExec(ctx, ref Ref, ExecRequest) (ExecHandle, error) // закрепляет экземпляр
}
type ExecHandle interface {
    Describe() LiveTarget                       // target, сервер, экземпляр, канал, argv
    Run(ctx, Terminal) (ExitStatus, error)      // Terminal{Stdin, Stdout, Sizes}
    Again() (ExecHandle, error)                 // «Подключиться заново»: тот же снимок и цель
    Close()
}
type PortForwarder interface {
    ForwardInfo(ctx, ref Ref) (ForwardInfo, error)
    PrepareForward(ctx, ref Ref, ForwardRequest) (ForwardHandle, error) // закрепляет цель
}
type ForwardHandle interface { Describe() LiveTarget; Connect(ctx) (Upstream, error); Close() }
type Upstream interface {                       // одно соединение, много потоков
    Label() string; Open(ctx) (Stream, error); Done() <-chan struct{}; Err() error; Close()
}
type Stream interface { io.ReadWriter; CloseWrite() error; Close() error; Result() error }
// P4 (KindDescriptor.Actions: id, заголовок, destructive, параметр count min..max).
// Протокол без состояния на сервере: план → подтверждение в UI → выполнение плана.
type Actioner interface {
    // Только чтение: где (target, сервер, ref с UID), последствия, предупреждения,
    // права (allowed/denied/unknown), Unavailable, Current (scale), Destructive плана,
    // Expect — непрозрачный отпечаток действия, параметров и состояния, от которого
    // зависят последствия.
    PrepareAction(ctx, ref Ref, action string, p ActionParams) (ActionPlan, error)
    // Строго по UID; Expect не совпал → conflict; заменён/удалён → gone;
    // отправлено без ответа → unknown (не повторяется).
    RunAction(ctx, ActionRun{Ref, Action, Params, Expect}) (ActionResult, error)
}
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
| Metrics | metrics.k8s.io | stats API (двухточечный замер демона, `stream=false`) |
| Actions | restart, scale, delete | restart, stop/start, rm; scale/create — только при известной compose-модели |

Уточнение после ревью (2026-09-29): наблюдения Docker Engine — не желаемая модель Compose.
По labels видны только существующие контейнеры: сервис без контейнеров не обнаружить,
желаемое число реплик неизвестно. Поэтому сервис — синтетическая сущность с устойчивым id
(endpoint/project/service), контейнеры — её воплощения; «желаемое» показывается как
неизвестное, пока нет compose-файла; scale/create включаются только при доступной
compose-модели. Images и внешние networks/volumes не принадлежат проекту — связи
many-to-many. `docker events` хранит лишь последние 256 событий: поток событий — подсказка,
источник правды — список + пересверка изменённых id (после переподключения — полный
relist); медленные inspect — в ограниченном пуле, ответы старой эпохи в новую не попадают
(реализация — P6, решение 2). Это ложится на тот же
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
5. **Недавний рестарт — по времени API, без истории наблюдений (P5, пересмотрено
   2026-09-30).** Предыдущий экземпляр контейнера (обычного, init, sidecar) завершился в
   последние 10 мин (`lastState.terminated.finishedAt` — время завершения, не рестарта) →
   warning с причиной/кодом и накопительным `restartCount` API; истекает дедлайном; время
   впереди больше чем на минуту (перекос часов) перепроверяется, когда локальные часы
   догонят. То же для Warning events: одно событие = одна строка, счётчик накопительный,
   окно 15 мин по времени последнего наблюдения. Прежний план — история наблюдений
   `restartCount` (база, дельты, кольцо, разрывы) для «≥ N рестартов за окно» — **отложен в
   бэклог**: ревью Codex показало, что без одной авторитетной ленты наблюдений на pod
   (разные кэши namespace/все/имя, разрывы watch и relist) оценка скорости выдумана. Это
   сокращение объёма, решение агента; пользователь может вернуть требование.
6. **Problems — «проблемы в наблюдаемой области».** Открытый вид Problems берёт аренды
   минимальных детекторов (pods, workloads, nodes, events) для выбранной области и честно
   показывает покрытие; эти аренды входят в тот же бюджет. Связи (Deployment → ReplicaSet →
   Pod) тоже берут нужные внутренние наблюдения (ReplicaSet), а не зависят от того, какие
   таблицы пользователь открывал.
7. **Одна активная сессия просмотра.** Выбор target-а закрывает сессии остальных (кэши, виды,
   логи); терминалы и туннели — живые ресурсы приложения, они переживают выбор другого target-а
   и пересоздание сессии (как запущенный `kubectl port-forward`) и закрываются явно или при
   выходе; вкладка чужого target-а видимо помечена его именем (уточнено в P3, 2026-09-29).
   Сессия без видов закрывается через 60 с (страница ушла, вид «осиротел»); UI переключает выбор сразу,
   не дожидаясь ответа, — иначе старая страница успевала переоткрыть вид на закрытой
   сессии (найдено замером). Список scopes — живой вид kind, названного провайдером
   (`ScopesView.Kind`, k8s: namespaces).
8. **Действия (P4).** Двухшаговый протокол без серверных токенов: `PrepareAction` читает план
   (последствия, права, `Expect`), UI показывает его — context, сервер, namespace, вид, имя — и
   после подтверждения отправляет план обратно с `ConfigRev` target-а. `RunAction` перечитывает
   объект строго по UID, сверяет `Expect` (изменились replicas/политика PVC/пауза — `conflict`,
   «Проверить заново») и пишет с предусловиями UID + `resourceVersion`: restart — merge patch
   `kubectl.kubernetes.io/restartedAt` (RFC3339Nano), scale — merge patch subresource `scale` с
   UID + `resourceVersion`, delete — `Preconditions{UID, RV}` + фоновый каскад. Повтор (≤ 3) — только когда
   перечитывание доказывает, что записи не было (тот же UID и `Expect`, другая версия), и
   только отказ предусловия; запись — один HTTP-запрос без встроенных повторов client-go.
   Неоднозначный ответ (5xx, таймаут, шлюз, обрыв, в т. ч. транспорта UI) — `unknown`
   («проверьте, прежде чем повторять»). Последствия — честные и
   по стратегии («запрошено», «может»; политика PVC StatefulSet, контроллер pod-а, HPA), права —
   `SelfSubjectAccessReview`, «не удалось проверить» ≠ «разрешено». Подтверждение — только UX:
   все contexts RW (решение пользователя 2026-09-30).
9. **Никаких фоновых опросов в ядре.** Фича, требующая постоянного опроса кластера, в ядро не
   попадает; метрики опрашиваются только для видимой таблицы.

## Состояние (SQLite)

`~/.spk/ocular/ocular.db`, `modernc.org/sqlite`, WAL, одно соединение, встроенные миграции
`internal/store/migrations/NNNN_*.sql`, многошаговые записи только через `Store.WithTx`
(паттерн spk-mm-client). Таблицы: `ui_prefs` (выбранный target — `selected_target`) и
`target_state` (provider, target, key → JSON: последний `kind` и `scope` — с P1; ширины и
порядок колонок — позже), `recent_objects` (P5: недавние объекты для палитры — provider,
target, kind, scope, name, uid, title, opened_at; ≤ 50 на target, ≤ 500 всего, запись при
успешном открытии деталей; UID в идентичности — одноимённая замена даёт новую запись).
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
- **P3** ✅ — терминалы и туннели. Терминал: `S` / `Shift+S` / «Terminal» в деталях, выбор pod-а,
  контейнера или своей команды (argv без shell), xterm в ленивом чанке, двусторонние кредиты,
  «Подключиться заново» тем же снимком и pod-ом, вкладки в общей нижней панели переживают смену
  target-а. Туннели: секция «Ports» в деталях (Pod, Service с разрешением targetPort, workload),
  loopback 127.0.0.1 + ::1, «⇄ N» в строке состояния, «Open» только для web-портов, уход с
  удалённого pod-а, собственный сторож живости. Проверено на kind (оба транспорта, RBAC без
  `pods/exec`/`pods/portforward`, удаление pod-а, простаивающий туннель 35 с, shell-ы не остаются
  после закрытия вкладки и выхода) и в desktop под Xvfb: ввод, кириллица, `S` в русской раскладке,
  resize dock-а и окна, Ctrl+Shift+C/V, 50 МБ вывода со вставкой 2 МБ — Ctrl+C доходит за 0,18 с;
  Private_Dirty всех процессов 109 МБ → пик 314 МБ во время 50 МБ (мусор JS в WebProcess) → 147 МБ
  после (полный scrollback 5000 строк); терминал + туннель в покое — 105 МБ; выход 0,18 с, порты
  свободны.
- **P4** ✅ — действия (2026-09-30): restart Deployment/StatefulSet/DaemonSet, scale
  Deployment/StatefulSet (0..10000, в два шага: число → просмотр), delete pods, workloads,
  ReplicaSets, Services, Ingresses, ConfigMaps, Secrets. Меню «Действия» в деталях (текущий
  объект после переходов), контекстное меню строки (правый клик, `Shift+F10`, клавиша меню),
  `Delete` на таблице; диалог с где/что/правами, красная кнопка и фокус на «Отмена» у опасного
  плана, один запрос на подтверждение, `unknown`/`conflict` в диалоге, уведомление в строке
  состояния. Проверено на kind (замена объекта между чтением и записью, смена только status,
  чужое изменение replicas, политика PVC StatefulSet при scale down и delete, paused, HPA,
  RBAC viewer) и в desktop под Xvfb (контекстное меню, диалоги, `Delete`/`Shift+F10` в русской
  раскладке, фокус и Esc).
- **P5** ✅ — Problems, палитра, клавиатурная навигация, полировка, soak-замер памяти
  (реализовано 2026-09-30; soak принят 2026-09-30 на сборке P7: по фазам ≤ +75 МБ, после
  нагрузки 156 МБ ровно — план P5 Task 8). Problems: один вид из обычных наблюдений pods,
  workloads, services, ingresses, nodes и Warning events текущей области, ошибки → предупреждения
  → «недавнее» (рестарт за 10 мин, событие за 15 мин, истекают дедлайнами), покрытие по
  источникам («Не видно: …»), действия и логи строки — по её объекту. Палитра `Ctrl+K`: виды,
  target-ы, namespaces, строки текущей таблицы и недавние объекты (SQLite), свой fuzzy-скорер
  (кириллица как есть), команды `:ns <имя>`/`:ns *`, `:ctx <текст>`, `:<вид> [фильтр]` с
  алиасами kubectl — алиасы задаёт провайдер; команда срабатывает только при одном точном
  совпадении. Клавиатура: один реестр и справка `?`, `F6`/`Shift+F6` по областям, стрелки в
  навигации, PageUp/PageDown/Home/End в таблице, `Alt+←` и хлебные крошки в деталях, возврат
  фокуса; терминал получает все клавиши. Полировка: единственное число вида в диалогах,
  последствия и причины — ключи с параметрами (русский в UI), уровни exec от провайдера,
  поздний ответ действия после таймаута. Проверено: vitest, synth e2e, kind (Problems) и
  desktop под Xvfb в русской раскладке.
- **P6** ✅ — Docker Compose provider: просмотр и логи (реализовано 2026-09-30,
  `docs/plans/2026-09-30-p6-docker-compose.md`). Target-ы — Docker contexts, как docker CLI 29
  (`DOCKER_CONFIG`/`DOCKER_HOST`/`DOCKER_CONTEXT`, TLS-пары, inotify), рядом с kube contexts; свой
  тонкий клиент Engine (unix/tcp/TLS, без повторов). Scope — compose-проект; виды services
  (синтетические, по label-ам существующих контейнеров; желаемое — неизвестно), containers,
  networks, volumes, images; health — state + healthcheck; связи owns/uses/used-by; детали —
  факты, YAML inspect (свежий `Get`). Наблюдение — best effort: лента на тип объекта с эпохами
  (info → events → list → inspect → сверка изменённых id), событие — только подсказка, после
  обрыва — новая эпоха, «Перечитать» (`F5`) — явная пересверка пользователя; фоновых опросов
  нет. Логи контейнера и сервиса целиком: каналы stdout/stderr, источники по членам,
  продолжение по позиционному `since` Docker без повторов и пропусков, ожидание старта
  остановленного. Общий UI очищен от Kubernetes-знания (вид/область по умолчанию, вид событий,
  подписи scope — метаданные провайдера). Проверено: фейковый Engine, изолированный демон
  `ocular-dind` (Go-тесты и e2e), desktop под Xvfb в русской раскладке; Private_Dirty с
  открытыми kind и Compose и живыми логами — 132.9 МБ.
- **P7** ✅ — Compose: терминал, статистика, действия (2026-09-30,
  `docs/plans/2026-09-30-p7-compose-exec-stats-actions.md`). Терминал контейнера и
  контейнера сервиса (выбор реплики), exec через `net/http` 101, закреплённый контейнер и
  соединение, новый exec на запуск. Метрики — видимые строки для обоих provider-ов (`seq`,
  один запрос в полёте на вид и в Go, отмена в Go); Compose — CPU в ядрах по двухточечному
  замеру демона при той же инкарнации, память как `docker stats`, сервис — сумма ≤ 20
  («≥»). Действия: контейнер restart/stop/start/remove, сервис restart/stop/start; свежий
  список и Expect перед первой записью, члены по очереди до первого отказа, итог по частям
  (`ActionResult.Parts`). Проверено: фейковый Engine, `ocular-dind` (Go и e2e), kind e2e,
  desktop под Xvfb в русской раскладке; Private_Dirty с терминалами и метриками —
  110.9–124.0 МБ.

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
