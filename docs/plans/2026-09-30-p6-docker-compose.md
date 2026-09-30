# P6 — Docker Compose provider

> Для агентов: задачи выполняются по порядку, шаги отмечаются `- [x]`; каждая задача —
> отдельные коммиты `area: summary`, `make check` зелёный перед коммитом; каждое исправление и
> каждая возможность начинаются с теста, который падает без них.

**Цель:** в списке target-ов рядом с kube contexts — Docker contexts (Docker Engine endpoint-ы).
Открыв один, пользователь видит compose-проекты как scopes и в них — сервисы, контейнеры,
сети, тома, образы с живым состоянием и health (state + healthcheck), детали (факты, YAML
inspect, связи), логи контейнера и сервиса целиком, терминал в контейнере, CPU/RAM видимой
таблицы и действия restart/stop/start/remove с тем же подтверждением, что у Kubernetes. Всё —
через те же `core`/`provider`/общий UI: второй провайдер доказывает абстракцию.

**Спецификация:** `docs/specs/2026-09-29-spk-ocular-design.md` — «Проверка абстракции на
Docker Compose» (таблица соответствий и уточнение после ревью), «Provider API», «Живые
таблицы» (контракт Watch), «Ключевые технические решения» (время без опроса, одна активная
сессия, действия, никаких фоновых опросов), бэклог (долг общего UI из ревью P5).

## Глобальные ограничения

- Всё из P0–P5 (AGENTS.md «Правила»). В `internal/core`, `internal/api` и общем UI не
  появляются container/compose/project; Kubernetes-знание, оставшееся в общем UI (вид `pods`
  по умолчанию, `defaultNamespace`, связанный вид `events`, подпись scope в диалоге), уходит
  в метаданные провайдера (Task 1).
- Никаких фоновых опросов демона: состояние — список + поток `/events` как подсказка;
  статистика (CPU/RAM) — только для видимой таблицы, как метрики Kubernetes.
- Credentials (TLS-ключи context-а) не покидают Go: нет в БД, DTO, логах, событиях.
- Все запросы к демону — с контекстом и таймаутом; у потоков (events, logs, exec) — дедлайн
  только на заголовки, как у watch Kubernetes.
- Тесты с записью и нагрузкой — **только на изолированном тестовом демоне** (Task 0), никогда
  на демоне пользователя: там идут его контейнеры (compose-проект SPK_launcher и др.).
  Endpoint тестового демона проверяется до любой мутации, как kubeconfig kind.
- UX важнее экономии памяти; считается только Private_Dirty.
- Вне P6 (бэклог): чтение compose-файлов (желаемое число реплик, сервисы без контейнеров,
  scale/create/up/down), endpoint-ы `ssh://` (нужен `docker system dial-stdio`), Problems для
  Compose, Docker provider для контейнеров вне compose, Swarm, podman, build, push/pull образов,
  events как вид.

## Что уже есть (обзор 2026-09-30)

- Контракт `provider.Session` (Watch/Get/Scopes/ScopeKind), опциональные `LogSource`,
  `Execer`, `MetricsSource`, `Actioner`, `TargetWatcher`, `CommandAliaser` — всё это Compose
  реализует; `PortForwarder` — нет (порты уже опубликованы).
- `internal/views` (hot-layer видов), `internal/streams` (логи и терминалы к UI), менеджер
  сессий API — provider-агностичны; синтетический провайдер показывает, что UI обходится без
  Kubernetes, но в `Workspace`/`ResourceDrawer`/`ActionDialog` остались вшитые `pods`,
  `events`, `defaultNamespace`, `action.namespace` (ревью P5).
- На машине: Docker 29.3.1 (API 1.54), Compose v5.1.1, текущий context `rootless`
  (`unix:///run/user/1000/docker.sock`), `default` — `unix:///var/run/docker.sock` (нет
  сокета). Образ `docker:29-dind` есть локально.

## Решения P6 (агент, 2026-09-30)

### Клиент Docker Engine — свой, минимальный

Свой HTTP-клиент поверх `net/http` (`internal/providers/compose/engine`), а не
`github.com/docker/docker/client`: нужны ~15 вызовов (`_ping`, containers list/inspect,
networks, volumes, images, events, logs, stats, exec create/start/resize/inspect, restart/stop/
start/remove), а у официального клиента тяжёлые зависимости, неявные повторы и таймауты,
которыми мы не управляем (урок client-go из P4). Транспорт: `unix://` и `tcp://` (+TLS из
context-а: `ca.pem`/`cert.pem`/`key.pem`). Версия API — из заголовка `Api-Version` ответа
`/_ping`, пути `/v<min(своя, демона)>/…`; минимально поддерживаемая — 1.41 (Docker 20.10),
ниже — `unsupported`. Ошибки: JSON `{"message"}` + статус → классы (`404` not_found, `409`
conflict, `401/403` forbidden, `5xx`/обрыв — unavailable, у записи — unknown).
Потоки: `logs` без TTY — мультиплекс stdcopy (8-байтовый заголовок), с TTY — сырой поток;
`exec start` — hijack (`Upgrade: tcp`, 101) с отменяемым рукопожатием (как `upgrade.go`).

### Target-ы: Docker contexts

Источники как у docker CLI: `DOCKER_HOST` (тогда один target `env:DOCKER_HOST`, как делает
CLI), иначе contexts из `~/.docker/contexts/meta/*/meta.json` плюс встроенный `default`
(`unix:///var/run/docker.sock`); текущий — `DOCKER_CONTEXT`, иначе `currentContext` из
`~/.docker/config.json`. Id target-а — `context:<имя>` (стабилен). `ConfigHash` — endpoint +
`SkipTLSVerify` + содержимое TLS-файлов (HMAC как у kubeconfig, в UI — только `configRev`).
`ssh://` — target виден, сессия отвечает `unsupported` с объяснением. Изменения — inotify на
`~/.docker/contexts/meta` и `config.json` (`TargetWatcher`), без опроса. `Discover` — только
локальные файлы.

### Модель: что показывается

Scope — compose-проект (label `com.docker.compose.project`); виды (`KindDescriptor`, группы
навигации «Compose» и «Engine»):
- `projects` — строки-scopes (живой список scopes, `ScopeKind`), синтетические: имя, число
  сервисов/контейнеров, запущено, рабочий каталог и файлы (`…project.working_dir`,
  `…project.config_files` — только текст).
- `services` — синтетическая сущность, id `<project>/<service>` (устойчив, не зависит от
  контейнеров): контейнеры сервиса (не one-off), запущено/всего, health — по контейнерам;
  «желаемое» не показывается (неизвестно без compose-файла).
- `containers` — id контейнера = UID строки (пересоздание = новая строка, как замена pod-а);
  имя, сервис, номер реплики, state/status, health (healthcheck), рестарты (`RestartCount`),
  образ, порты (опубликованные), возраст, CPU/RAM.
- `networks`, `volumes` — проектные (по label), в «все проекты» — все; `images` —
  без scope (`Scoped: false`), связи many-to-many (образ ← контейнеры).
Контейнеры без compose-labels не показываются (это будущий Docker provider); в «все проекты»
видны только compose-контейнеры — `KindDescriptor.NotCovered` так и говорит.

### Health

Контейнер: `running` + health `healthy`/нет healthcheck → ok; health `starting` →
progressing; `unhealthy` → error (с последним выводом проверки, `Since` — время последней
неудачной проверки); `restarting` → warning (`RestartCount`); `exited` с кодом ≠ 0 → error
(`Since` = `FinishedAt`), с кодом 0 — ok «Completed» (one-off/init-подобные), `dead` → error,
`paused` → warning «Paused», `created` → progressing; `removing` → terminating. Недавний
рестарт — как в Kubernetes, из `State.StartedAt` при `RestartCount > 0`: warning 10 мин с
дедлайном. Сервис: худший из контейнеров, «0 running из N» → error, частично → warning.

### Наблюдение: список + события + пересверка

Одна «лента» на сессию и вид объектов (контейнеры; сети/тома/образы — отдельные, лениво по
первому виду): `GET /containers/json?all=1&filters=label=com.docker.compose.project` →
снимок; затем `/events?filters=type=container&event=<create,start,die,stop,kill,pause,
unpause,restart,rename,update,destroy,health_status>` (с `since` = время снимка; `exec_*` от
healthcheck-ов отфильтрованы — спайк) — **подсказка**:
по событию id ставится в очередь пересверки (`inspect` в ограниченном пуле, поколение на id:
поздний старый ответ не перетирает новый; 404 = удаление). Обрыв потока — переподключение
с backoff; поток, не восстановленный за 5 с, делает виды stale; после переподключения —
полный relist со сравнением (пропавшие — удаления). Ready — после снимка и inspect-ов, все
начальные строки дошли до вида. Инвариант как у informer-а: один затвор порядка на вид,
дедлайны (недавний рестарт) принадлежат инкарнации (id). `docker events` хранит лишь 256
последних событий — поэтому relist после каждого переподключения, а не догон по `since`.

### Логи, терминал, статистика, действия

- Логи: контейнер (`timestamps=1`, `tail`, `since`, `follow`; stdout и stderr — два
  источника одного канала, `stderr` виден префиксом источника, порядок — по меткам времени);
  сервис — агрегат по его контейнерам (живой набор, как логи Deployment: новый
  контейнер подключается, удалённый — ended), те же лимиты `streams`. «previous» — нет
  (у Docker один журнал на контейнер; перезапущенный контейнер продолжает его).
- Терминал: `Execer` — экземпляры = контейнеры сервиса (или сам контейнер), канал один;
  команда по умолчанию — `sh` (проба `bash`→`sh` не делается: argv явный); hijack, resize,
  код выхода из `exec inspect`; закрытие «вешает трубку» так же (`^C`/`^D`).
- Статистика: `MetricsSource` для видов containers/services — `stats?stream=false` по видимым
  строкам в пуле ≤ 8, CPU из дельты `cpu_stats`/`precpu_stats`, память — `usage - cache`
  (`inactive_file`), по id (инкарнации) контейнера.
- Действия (`Actioner`): контейнер — restart, stop, start, remove (только остановленный;
  запущенный — `Unavailable` «сначала остановите»); сервис — restart/stop/start всех его
  контейнеров. `Expect` — id(ы) и `State.StartedAt`/статус; запись одна, без повторов
  (`unknown` при обрыве). Последствия: «контейнер будет пересоздан? — нет: restart сохраняет
  контейнер», remove — «тома с данными остаются» (анонимные тома — `v=false`).
  Подтверждение, права (у Docker нет SSAR — права «неизвестно»/доступ к сокету) — через
  общий диалог.

### Тестовый демон (аналог kind)

Изолированный Docker Engine в контейнере `ocular-dind` (`docker:29-dind`, `--privileged`,
TCP только на `127.0.0.1`, без TLS), образы загружаются в него из локального демона
(`docker save | docker -H … load`, без сети). Скрипты `scripts/dind-up.sh`/`dind-down.sh`/
`dind-seed.sh` (compose-проект фикстур: healthy, unhealthy, crashloop, exited, one-off,
сеть/том), цели `make test-dind`, `make e2e-dind`; мутации — только если endpoint = этот
контейнер. Если dind не поднимается под rootless docker (Task 0), запасной вариант —
отдельный rootless-демон пользователя не трогаем: тогда только фейковый Engine (httptest по
unix-сокету) + ручная проверка чтения на демоне пользователя **без записи**.

## Задачи

### Task 0. Спайк: dind под rootless docker, API Engine
- [x] `ocular-dind` поднимается под rootless docker за ~2 с (~90 МБ), образы загружаются без
  сети, compose-проект фикстур работает — `docs/spikes/2026-09-30-dind.md`.
- [x] Живой API: формат `/events`, healthcheck шумит `exec_*` (фильтр по действиям),
  мультиплекс логов, `stats?stream=false` ~1 с. Hijack exec — проверяется тестом Task 5.
- [ ] `scripts/dind-up.sh` (контейнер, порт, загрузка образа, проверка endpoint-а),
  `dind-seed.sh` (проект фикстур), `dind-down.sh`; цели `make dind-up`/`dind-down`.

### Task 1. Общий UI без Kubernetes-знания
- [ ] `KindDescriptor`/провайдерские метаданные: вид по умолчанию, scope по умолчанию
  (k8s: `defaultNamespace` context-а — из `Target.Details` уходит в явное поле), связанный вид
  событий объекта (`EventsKind`, "" = нет секции), подпись scope в диалоге действия
  (`ScopeTitle`: «Namespace»/«Project»). Тесты: синтетический провайдер без событий и с другим
  видом по умолчанию — UI не открывает `pods`/`events`.

### Task 2. Discovery: Docker contexts
- [ ] `internal/providers/compose`: источники (DOCKER_HOST, DOCKER_CONTEXT, config.json,
  meta.json, default), target-ы, `ConfigHash` с TLS-файлами, `ssh://` — unsupported,
  проблемы битых meta.json; inotify-watch. Тесты на фикстурных `~/.docker`; e2e: contexts
  видны рядом с kube contexts, секретов на странице нет.

### Task 3. Клиент Engine
- [ ] `engine`: транспорт unix/tcp/TLS, `_ping` и версия, ошибки → классы, дедлайны (запрос /
  заголовки потока), декодеры list/inspect/events, stdcopy, hijack. Фейковый Engine
  (`enginefake`): unix-сокет, сценарии (обрыв потока, медленный inspect, 404, 5xx, старый API).

### Task 4. Наблюдение и виды
- [ ] Лента контейнеров (снимок, события-подсказки, пул inspect с поколениями, relist после
  переподключения, stale через 5 с), виды `projects`/`services`/`containers`/`networks`/
  `volumes`/`images`, health и дедлайны, Scopes/ScopeKind, Get (факты, YAML inspect, связи
  контейнер↔сервис↔сеть/том/образ). Тесты на фейке: пересоздание с тем же именем — новая
  строка; поздний inspect старого поколения не воскрешает; потеря событий — relist чинит;
  unhealthy/exited/restarting. dind: `TestDind*` — живые изменения при `compose up/down/
  restart`, остановка демона → stale → восстановление.

### Task 5. Логи и терминал
- [ ] `LogSource` (контейнер, сервис-агрегат), `Execer` (hijack, resize, код выхода, «вешание
  трубки»). Тесты на фейке + dind: логи переживают рестарт контейнера, новый контейнер
  сервиса подключается; shell не остаётся после закрытия вкладки.

### Task 6. Статистика и действия
- [ ] `MetricsSource` (видимые строки, пул), `Actioner` (restart/stop/start/remove контейнера,
  restart/stop/start сервиса; Expect; unknown без повторов). Тесты: заменённый контейнер —
  gone; изменившееся состояние — conflict; запущенный remove — Unavailable. dind: действия
  на фикстурах.

### Task 7. UI, desktop, документы, ревью
- [ ] Регистрация провайдера, e2e на dind (`playwright.dind.config.ts`), desktop под Xvfb
  (русская раскладка): contexts, проекты, сервисы, логи сервиса, терминал, действие —
  скриншоты; замер памяти (Private_Dirty) с открытым Compose target-ом.
- [ ] AGENTS.md, спецификация (P6 ✅), бэклог, «Итоги»; гейты `make check`, kind, dind.
- [ ] Ревью реализации Codex, исправления, раздел «Ревью реализации».
