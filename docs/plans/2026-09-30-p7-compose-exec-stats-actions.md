# P7 — Docker Compose: терминал, статистика, действия

> Для агентов: задачи выполняются по порядку, шаги отмечаются `- [x]`; каждая задача —
> отдельные коммиты `area: summary`, `make check` зелёный перед коммитом; каждое исправление и
> каждая возможность начинаются с теста, который падает без них.

**Цель:** в открытом Docker context-е пользователь открывает терминал в контейнере или в
контейнере сервиса (`S`/`Shift+S`, «Терминал» в деталях), видит CPU и память контейнеров и
сервисов в таблице и выполняет restart/stop/start/remove контейнера и restart/stop/start
сервиса — с тем же планом, подтверждением и честными исходами (`unknown`, `conflict`), что у
Kubernetes. Всё — через общие `provider`/`api`/UI; Kubernetes-поведение не меняется, кроме
контракта метрик (решение 3), который меняется для обоих.

**Спецификация:** `docs/specs/2026-09-29-spk-ocular-design.md` — «Provider API» (Execer,
MetricsSource, Actioner), «Проверка абстракции на Docker Compose» (Metrics: stats API,
Actions: restart/stop/start/rm), «Ключевые технические решения» (действия: план → подтверждение
→ запуск по UID и Expect, `unknown` без повторов; никаких фоновых опросов). Решения P7,
принятые на ревью плана P6, — конец `docs/plans/2026-09-30-p6-docker-compose.md`; здесь они
развёрнуты до реализуемых.

## Глобальные ограничения

- Всё из P0–P6 (AGENTS.md «Правила»). В `internal/core`, `internal/api` и общем UI не
  появляются container/compose/project; тексты, завязанные на Kubernetes («pod», «container»
  в общих подписях), уходят в метаданные провайдера.
- Клиент Engine ничего не повторяет: запрос, который мог дойти до демона, при сбое —
  `unknown`, не повтор. Чтения (inspect, stats, exec inspect) — с таймаутом и ограничением
  размера ответа, как в P6.
- Никаких фоновых опросов: статистика — только пока видна таблица с колонками метрик (UI уже
  приостанавливает опрос при скрытой странице), только видимые строки.
- Credentials не покидают Go (TLS-пары context-а).
- Тестовые записи — только на `ocular-dind` (`scripts/dind-verify.sh` перед каждой мутацией)
  и kind `ocular-dev`; контейнеры пользователя не трогаются.
- UX важнее экономии памяти; считается только Private_Dirty.

## Что уже есть (обзор 2026-09-30, карта — отчёт исследования)

- Kind-флаги: `KindDescriptor.Exec`, `.Actions`; метрики — `Column.Metric` (UI опрашивает, если
  у вида есть метрическая колонка; `Workspace.tsx:294`, `useMetrics.ts`: сразу, потом каждые
  15 с от конца прошлого вызова, пауза при `document.hidden`, `unsupported` — раз в 120 с).
- Терминал: `provider.Execer`/`ExecHandle` (Run/Again/Close, хэндл не зависит от сессии),
  `api/exec.go` (прототипы ≤ 64, `startTerminal` = `Again()` + `streams.RegisterTerm`),
  WebSocket-протокол `streams/term.go` (окна 1 МиБ/256 КиБ, `exit{code}` только при
  `Known`, завершение — ^C, ^D, затем отмена). UI: `TerminalDialog` (экземпляры/каналы,
  подписи от провайдера, `noInstances`), `TerminalView` (заголовок `channel · instance`).
- Метрики: `MetricsSource.Metrics(ctx, q)` — по всему запросу вида; `api.GetMetrics(viewID)`
  отдаёт значения текущих строк вида; UI рисует пустую ячейку для «нет значения».
- Действия: `core.ActionPlan` (Where, Effects/Warnings — `Message`, Rights, Unavailable,
  Expect), `core.ActionResult{Message}`, `api.RunAction` (UID, Expect, ConfigRev обязательны),
  `ActionDialog` (гонка с 60 с, поздний ответ, `unknown`/`conflict`/failed), клавиша `Delete` —
  действие с id `delete`, переводы `act.*` — только restart/scale/delete.
- Клиент Engine P6 — только GET и потоки (list/inspect/events/logs); POST, exec, hijack и stats
  нет. `enginefake` — модель объектов, события, журнал логов.
- SPK-launcher `internal/docker/stats.go` (код пользователя, переносится): эффективная
  память `usage − inactive_file`/`total_inactive_file`/`cache`, CPU из cpu/precpu.

## Решения P7

### 1. Клиент Engine: записи и exec

- **POST-вызовы** (`post(ctx, path, q, body, limit)`): тело JSON ≤ 64 КиБ, ответ ограничен;
  классификация результата: ошибка **до отправки** (dial, TLS, запись заголовков) — обычный
  класс (`unavailable`…), ответа нет **после** возможной отправки (обрыв при чтении ответа,
  таймаут после записи) — `ClassUnknown`; 304 — отдельный признак «ничего не изменилось»
  (`NotModified`), не ошибка; 404 → `not_found`, 409 → `conflict`, прочие 4xx → `bad_request`
  с текстом демона, 5xx → `unavailable` (**после** отправки — `unknown`, как у Kubernetes:
  демон мог успеть). Граница «отправлено» — `httptrace.WroteHeaders`/`WroteRequest`.
- **Exec**: `CreateExec(ctx, id, ExecConfig{Cmd, Tty: true, AttachStdin/Stdout/Stderr,
  ConsoleSize})` → exec id; `StartExec(ctx, execID, size)` — `POST /exec/{id}/start` с
  `Connection: Upgrade`, `Upgrade: tcp` через обычный `http.Transport`: на 101 тело ответа —
  `io.ReadWriteCloser`, байты, прочитанные сверх заголовков, сохраняются (`net/http`
  `newReadWriteCloserBody`), соединение принадлежит вызывающему (`errCallerOwnsConn`) — поэтому
  прокси, TLS и отмена рукопожатия на всех шагах (dial, TLS, запись, заголовки) — от `net/http`
  и контекста запроса с дедлайном заголовков; после 101 отмена привязывается `context.AfterFunc`
  → `Close`. Своего `upgrade.go` (как у Kubernetes) не нужно. Ответ не 101 (200 без upgrade у
  старого демона, 404 exec, 409 «container is paused/not running») — ошибка с классом.
  `ResizeExec`, `InspectExec` (`ExitCode`, `Running`). Ограничения: 101 без тела ошибки — не
  «успех», пока поток не дал байт или exec inspect не сказал `Running`.
- **Stats**: `ContainerStats(ctx, id, oneShot bool)` — `GET /containers/{id}/stats?stream=false`
  (`one-shot=1` — сразу, без precpu; без него демон ждёт ~1 с и отдаёт precpu); ответ ≤ 256 КиБ.
- **Действия контейнера**: `RestartContainer(ctx, id, timeout)`, `StopContainer(ctx, id,
  timeout)` (`t=` явно; дедлайн запроса = timeout + 15 с), `StartContainer`, `RemoveContainer(ctx,
  id)` (`force=false`, `v=false`).
- `enginefake`: exec (create/start с hijack — сервер отдаёт 101 и эхо-shell / заданный
  сценарий, resize, inspect с кодом выхода; hooks: зависший 101, отказ до 101, обрыв посреди
  потока, «container is paused»), stats (заданные ответы по контейнеру, cgroup v1/v2), действия
  (меняют состояние, шлют события; hooks: 304, 409, обрыв после записи).

### 2. Терминал

- `Exec: true` у containers и services. `ExecInfo`: у контейнера — один экземпляр (он сам),
  у сервиса — его контейнеры (не one-off) в порядке номера, `Ready` = running и не paused,
  подписи уровней от провайдера («Контейнер»/«Container»); каналов нет — один канал `shell`
  (UI прячет выбор при одном канале), `NoInstances` — «у сервиса нет запущенных контейнеров».
  Instance id — **полный id контейнера**; явный экземпляр сверяется: он в сервисе (labels
  project/service), не one-off.
- Команда по умолчанию — `sh -c 'command -v bash >/dev/null 2>&1 && exec bash || exec sh'`
  (bash, если есть, иначе sh — один exec без проб); явная команда — argv как есть. Нет `sh` —
  класс `bad_request` «в контейнере нет shell» (ответ демона/код 126–127 до вывода).
- `PrepareExec` закрепляет контейнер (полный id), команду и **снимок соединения** (клиент
  Engine с TLS-парой — принадлежит хэндлу, закрывается его `Close`; сессия, закрытая сменой
  target-а, хэндл не ломает). `Run`: свежий inspect — удалён → `gone`, другой id под именем
  невозможен (id полный), не running/paused → `bad_request` с состоянием; **новый exec id на
  каждый Run/Again**; create → start (hijack) → копирование stdin/stdout, resize из
  `TermSizes` через `ResizeExec` (поздний resize после конца — игнор). Конец потока →
  `InspectExec`: `Running=false` → `ExitStatus{Code, Known: true}`; иначе/ошибка → `Known:
  false`. EOF ≠ код 0. После неоднозначного ответа `start` (обрыв до 101 после записи) — ни
  повтора, ни нового exec: ошибка `unknown` («команда могла запуститься»).
- `LiveTarget`: `Endpoint` — host Docker endpoint-а, `Instance` — имя контейнера (Title),
  `Channel` — пусто, `Command`.
- Общие слои: подписи «pod/container» в `api`/`core`/`TerminalDialog` — через уровни
  провайдера (уже есть `InstanceLabel`/`ChannelLabel`), комментарии — нейтральные; заголовок
  вкладки при пустом канале — только экземпляр.

### 3. Статистика (контракт меняется для всех провайдеров)

- **API**: `GetMetrics(viewID, rowIDs []string)` — UI передаёт видимые строки таблицы
  (виртуализатор без overscan, ≤ 60; больше — отсечение с сообщением), сервер проверяет, что
  каждая — строка вида (чужие — игнор), контекст — запроса страницы (отмена при уходе), общий
  дедлайн 10 с. `provider.MetricsSource.Metrics(ctx, q, rowIDs)`; Kubernetes фильтрует свой
  список metrics.k8s.io по rowIDs (кэш 10 с не меняется). UI: `useMetrics` получает видимые id
  от `ResourceTable` (по изменению видимого набора — запрос без ожидания 15 с, с дебаунсом
  300 мс), сортировка по метрикам — по известным значениям (невидимые — «неизвестно»,
  внизу).
- **Compose**: метрические колонки CPU и Memory у containers и services. Контейнер:
  `ContainerStats(one-shot)` с пулом 8 на сессию; CPU = (Δcpu контейнера / Δsystem) × online
  CPUs — **ядра**, по двум замерам: предыдущий замер сессии для той же инкарнации
  (`id` + `StartedAt`); нет предыдущего — один запрос без `one-shot` (демон отдаёт precpu),
  дельты неотрицательны, знаменатель > 0, иначе CPU «неизвестно». Память — `usage −
  inactive_file` (cgroup v2) / `usage − total_inactive_file` (v1), вне [0, usage] —
  «неизвестно». Время замера — `read` ответа; инкарнация не совпала (рестарт между замерами) —
  CPU «неизвестно» до следующего. Не running — нет значения. Только Linux-демоны
  (`/info OSType`), иначе `unsupported`.
- **Сервис**: сумма по членам (≤ 20 running), члены без значения делают итог **частичным**:
  новое поле `provider.Usage.Partial` (UI: «≥ 1.2», подсказка «не все контейнеры ответили»).
- Кэш предыдущих замеров — на сессию, по id контейнера, удаляется вместе с исчезновением
  контейнера из ленты (ограничен числом контейнеров).

### 4. Действия

- Контейнер: `restart`, `stop`, `start`, `delete` (заголовок «Remove»/«Удалить» — id `delete`,
  чтобы работали клавиша `Delete` и меню). Сервис: `restart`, `stop`, `start`.
- **План**: текущее состояние (running/exited/paused), последствия (`Message` с ключами:
  «контейнер будет остановлен сигналом SIGTERM, через N с — принудительно (SIGKILL)»,
  «restart policy перезапустит контейнер после stop, если …» — для `always`/`unless-stopped`
  не перезапустит после явного stop, для `on-failure` — нет; «том контейнера не удаляется
  (`v=false`)», «AutoRemove: контейнер исчезнет после остановки»); `Unavailable`: start
  запущенного, stop остановленного, remove запущенного («сначала остановите» — демон
  откажет); `Rights: unknown` («доступ к сокету Docker = всё; права не проверяются»).
- **Expect** (локальная предпроверка, не условие записи Engine): sha256 от (действие, для
  каждого члена отсортированно: полный id, `StartedAt`, status, `StopTimeout`, `StopSignal`,
  `AutoRemove`). `RunAction`: свежий inspect каждого члена → несовпадение — `conflict` до
  первой записи. Гонка «кто-то изменил контейнер между проверкой и записью» остаётся —
  сказано в диалоге (Warnings) и в документах.
- **Сервис**: набор членов **закреплён в плане** (полные id, отсортированы, в Expect), новые
  члены во время выполнения не добавляются; по одному запросу на член, последовательно;
  первый отказ или `unknown` останавливает остальные. Итог — `core.ActionResult.Parts
  []ActionPart{Title, Outcome done|refused|unknown|skipped, Message}` (новое поле; общий UI
  показывает список исходов в диалоге и в уведомлении — «2 из 3 выполнено, 1 не выполнялось»).
  Общий класс ошибки результата: всё сделано — успех; `unknown` у члена — ошибка `unknown` с
  Parts; отказ — ошибка с Parts.
- Таймаут stop — явный `t=` (из `StopTimeout` контейнера, иначе 10 с); дедлайн запроса —
  больше. 304 — «ничего не изменилось» (исход `done`, сообщение). Сбой до отправки — failed,
  после возможной отправки — `unknown`; повторов нет.
- i18n: `act.stop`, `act.start`, `act.remove`; ключи последствий `compose.*` в
  `providerTexts`.

### 5. Проверка на настоящем демоне

- dind (`make test-dind`): exec в `web` (busybox: sh есть, bash нет — выбирается sh; код
  выхода `exit 3`; resize; отмена), отсутствующая команда (`/nonexistent` → `bad_request`),
  stats `logger` (CPU > 0 в ядрах, память > 0), действия на `ocular-other`
  (stop → start → restart), remove — на одноразовом контейнере теста (`docker run` на dind
  после `dind-verify.sh`), сервис из 2 реплик (`logger`): stop обоих → Parts, start.
- e2e dind: терминал сервиса (выбор реплики, `echo`), колонки CPU/Memory, диалог stop/start
  контейнера с последствиями, Parts у сервиса.

## Задачи

### Task 0. Спайк на dind
- [ ] Exec через `net/http` 101 (tcp dind): байты после заголовков, отмена до/после 101,
  `exec inspect` после конца, resize, ответ на paused/stopped контейнер, код 126/127 для
  отсутствующей команды; stats `one-shot` и без (поля cgroup v2 dind, `online_cpus`,
  `precpu`); stop с `t=`, 304 у повторного stop/start, restart; remove запущенного — 409.
  Итог — раздел «Спайк» здесь.

### Task 1. Клиент Engine: POST, exec, stats, действия (решение 1)
- [ ] `engine/calls.go`/`exec.go`/`stats.go`; классы «до/после отправки», 304; `enginefake`
  exec/stats/действия с hooks. Тесты клиента на фейке: 101 с байтами в том же чтении,
  зависший 101 (дедлайн заголовков), отказ до 101, отмена после 101, обрыв после записи POST
  → `unknown`, 304.

### Task 2. Терминал Compose (решение 2)
- [ ] `compose/exec.go` (Execer), `Exec: true`; общие подписи. Тесты на фейке: контейнер и
  сервис, экземпляр не из сервиса, остановленный/на паузе/заменённый, код выхода и неизвестный
  код, неоднозначный start, закрытая сессия; e2e dind; kind-терминал не меняется (e2e-kind).

### Task 3. Контракт метрик: видимые строки (решение 3, общая часть)
- [ ] `GetMetrics(viewID, rowIDs)`, `Metrics(ctx, q, rowIDs)`, Kubernetes фильтрует; UI:
  видимые id из виртуализатора, запрос при смене набора; `Usage.Partial` в UI. Тесты: api
  (чужие id, > 60, отмена), vitest (видимые строки, смена прокрутки), kind (метрики pods как
  раньше).

### Task 4. Статистика Compose (решение 3)
- [ ] `compose/metrics.go`, колонки; CPU по двум замерам с инкарнацией, память v1/v2, сумма
  сервиса с Partial, не-Linux → unsupported. Тесты на фейке и dind.

### Task 5. Действия (решение 4)
- [ ] `core.ActionResult.Parts` + UI (`ActionDialog`, уведомление); `compose/actions.go`;
  i18n. Тесты: план (последствия, Unavailable), Expect/conflict, 304, unknown после записи,
  сервис — закреплённый набор, остановка на первом отказе, Parts; vitest диалога; dind; e2e
  dind.

### Task 6. Desktop, документы, ревью
- [ ] Desktop под Xvfb (русская раскладка): терминал контейнера и сервиса, CPU/Memory,
  диалоги действий и Parts — скриншоты; Private_Dirty с открытым терминалом и метриками.
- [ ] AGENTS.md, спецификация (P7 ✅), бэклог, «Итоги»; гейты `make check`, kind, dind.
- [ ] Ревью реализации Codex, исправления, раздел «Ревью реализации».

## Фокус ревью (что тесты задач могут не поймать)

1. Терминал сервиса, у которого реплика перезапустилась между выбором и запуском: должен
   открыться в **выбранном** контейнере или сказать «заменён», никогда — в другом.
2. Stop сервиса, во время которого появился новый член (scale извне): новый не трогается,
   Parts говорят о закреплённых.
3. Метрики при быстрой прокрутке большой таблицы: запросы не копятся (не перекрываются),
   значения не «прыгают» на чужие строки.
4. Обрыв соединения с демоном посреди `stop` с большим таймаутом: исход — `unknown`, не
   failed и не повтор.
5. Терминал, закрытый во время рукопожатия (101 ещё не пришёл): exec не остаётся висеть
   (соединение закрыто, `Run` вернулся быстро).
