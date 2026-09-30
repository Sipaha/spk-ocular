# spk-ocular — гид для агентов

Лёгкий локальный просмотрщик инфраструктуры: «Lens-like visibility + kubectl-like простота,
без тяжеловесности Lens». Provider-ы — Kubernetes и Docker Compose.
Go + Wails v3 + React. Спецификация: `docs/specs/2026-09-29-spk-ocular-design.md`.
Планы: `docs/plans/`. Бэклог: `docs/backlog.md`.

Статус: P0 (каркас), P1 (ресурсы, детали, метрики), P2 (логи), P3 (терминалы, туннели) и P4
(действия restart/scale/delete) готовы — `docs/plans/`. P5 (Problems, палитра `Ctrl+K`,
клавиатура, полировка) готов (`docs/plans/2026-09-30-p5-problems-palette.md`; soak памяти принят на
сборке P7 по критерию пользователя 2026-09-30). Docker Compose provider
готов: P6 — просмотр и логи (`docs/plans/2026-09-30-p6-docker-compose.md`), P7 — терминал,
статистика и действия (`docs/plans/2026-09-30-p7-compose-exec-stats-actions.md`). P8 — все
ресурсы API (discovery + server-side Table: CRD, Jobs, PVC…) — `docs/plans/2026-09-30-p8-generic-resources.md`.
P9 — правка YAML объекта Kubernetes с просмотром и одной записью — `docs/plans/2026-09-30-p9-edit-yaml.md`.

## Сборка и тесты

- `make build` — web + бинарь browser-режима `build/bin/spk-ocular` (CGO off).
- `make build-desktop` — desktop `build/bin/spk-ocular-desktop` (теги `wails gtk3`, CGO); собирается
  во временный файл и атомарно переносится (`mv`). `make release` — то же с тегом `production`
  (без DevTools).
- `make run` — `build-desktop` + запуск. `make run-browser` — UI на http://127.0.0.1:5190 (`PORT=`)
  с настоящим kubeconfig и `~/.spk/ocular`.
- `make test` = `test-go` (`go test -race`) + `test-web` (vitest) + `test-e2e` (Playwright против
  browser-режима с фикстурным `KUBECONFIG`/`HOME`/`SPK_OCULAR_HOME` в `tests/e2e/.run/`: два
  конфига — `playwright.config.ts` и `playwright.synth.config.ts`, последний запускает
  `--test-api --test-synthetic`: синтетический провайдер с логами (`POST /api/_test/logs/emit`),
  эхо-терминалом (`flood N`, `exit N`, `size CxR` при resize) и портами на встроенных
  HTTP-серверах; `GET /api/_test/stats` добавляет его счётчики `syn_*`).
- `make lint` — go vet и golangci-lint (с тегами desktop и без) + eslint + tsc.
- **`make check`** — гейт перед каждым коммитом: lint, все тесты, обе сборки.
- `make pss PID=<pid>` — Private_Dirty/PSS процесса и его WebKit-детей (бюджет ~150 МБ Private_Dirty).
- Реальный кластер (kind в Docker; `KIND=<путь>`, если kind не в PATH): `make kind-up` (kubeconfig —
  `build/kind-ocular-dev.kubeconfig`, никогда не в `~/.kube`), `make test-kind` (Go-тесты `*Kind*`),
  `make e2e-kind` (Playwright), `make kind-down`. Обе цели **падают**, если кластера нет, и сами
  сидируют фикстуры (`scripts/kind-seed.sh`, `kind-rbac.sh`, `kind-metrics.sh`). Нагрузка —
  `scripts/kind-load.sh <kubeconfig> [up|down]`, замер — `node tests/e2e/measure-kind.mjs <bin>
  <kind kubeconfig> <viewer kubeconfig> <scratch>` (печатает тайминги, счётчики `/api/_test/stats`
  и Private_Dirty). Синтетика памяти кэша: `OCULAR_SYNTH=1 go test -run Synthetic -v ./internal/providers/kubernetes/`.
- Тестовый Docker Engine (аналог kind для Compose): `make dind-up` (контейнер `ocular-dind`,
  `docker:29-dind` с label-ом `ocular.test=dind` в docker пользователя, API `tcp://127.0.0.1:23750`
  без TLS; id записан в `build/ocular-dind.id`; сид — `scripts/dind-seed.sh`: busybox через
  `docker save | load`, внешние `ocular-ext-net`/`ocular-ext-vol`, проекты `ocular-fixture`
  (healthy, unhealthy, crash-loop, exited 0/3, две реплики с логами, one-off) и `ocular-other`),
  `make test-dind` (Go-тесты `*Dind*`, `OCULAR_DIND_HOST`/`OCULAR_DIND_VERIFY`; **падает** без
  демона), `make e2e-dind` (Playwright `dind.spec.ts`: фикстурный `~/.docker` с context-ом
  `ocular-dind`), `make dind-down`. `/run` контейнера — tmpfs (переживает `docker stop/start`). Перед любой мутацией — `scripts/dind-verify.sh`: записанный
  контейнер наш (имя, label), запущен, публикует ровно `127.0.0.1:23750`, и `/info` на порту
  отвечает его hostname-ом; иначе отказ. Контейнер `ocular-dind` без label-а — не наш, скрипты
  его не трогают. Демон пользователя (его compose-проекты) — никогда.
- Soak: `scripts/kind-churn.sh <kind kubeconfig> up|run|pause|break|down` (namespace `ocular-churn`,
  ограниченный набор постоянно меняющихся объектов; отказывается работать не с kind-ocular-dev),
  desktop с `--test-api` (test-маршруты на отдельном loopback-порту, адрес и токен — в
  `<data>/test-api.json`, 0600, удаляется при выходе) и `scripts/soak-sample.sh <pid> <data> <csv>`
  (раз в `INTERVAL` с: MemAvailable, Private_Dirty/PSS/swap дерева, PID-ы, счётчики stats, фаза из
  `PHASE_FILE`; при MemAvailable < 8 ГБ отказывается — вытесненные страницы уходят из Private_Dirty).
  Вердикт — `scripts/soak-verdict.py <csv>` по критерию пользователя 2026-09-30 (спецификация,
  «Память»): по фазам ≤ +100 МБ от уровня после прогрева, после нагрузки ≥ 10 мин ровно.
- `SPK_OCULAR_KLOG=1` — вернуть логи client-go (klog) в stderr для отладки.
- `E2E_BIN=<путь> E2E_PORT=<порт>` — e2e против другой browser-сборки.
- Проверка desktop без экрана пользователя: `xvfb-run -a -s "-screen 0 1400x900x24" <скрипт>`
  (или `Xvfb :77 &` + `DISPLAY=:77`), окно ищется `xwininfo -root -tree | grep '"SPK Ocular"'`,
  снимок — `import -window root`, ввод — `scripts/xinput.py click X Y key l ctrl+s type TEXT`
  (XTest через ctypes; xdotool в окружении нет; ещё `drag`, `scroll`, `resize <окно> W H`,
  `close <окно>` — WM_DELETE_WINDOW как у кнопки закрытия, `group 1` — вторая раскладка после
  `setxkbmap -layout us,ru`). Окно без WM стоит в (60,40). «Открыть» туннеля в desktop зовёт
  `xdg-open` — подменяется скриптом первым в `PATH`; буфер обмена — `xclip -selection clipboard`.

## Устройство (коротко)

- `internal/api` — интерфейс `API` + DTO; `transport/http.go` (browser: `POST /api/<Method>`, SSE
  `/api/events`, bearer из `<meta name="spk-ocular-api-token">`), `transport/wails.go` (бинды,
  FQN `github.com/spk/spk-ocular/internal/api/transport.API.<Method>`). Новый метод API = метод в
  интерфейсе + `Service` + маршрут в `http.go` + метод в `wails.go` + `web/src/api/client.ts`.
  Ошибки HTTP-транспорта — статус 400 с классом в `code` (`forbidden`, `conflict`, `unknown`, …).
- `internal/events` — `Emitter` (почтовый ящик «последнее событие на (type, key)» у каждого
  подписчика, переполнение → `resync`) + `Coalescer`.
- `internal/views` — provider-агностичный hot-layer видов: `View` (строки, версии, надгробия,
  `Since(cursor)`), `Manager` (непереиспользуемые id, `view_changed` через Coalescer, аренды 60 с,
  `gone` при закрытии). Клиентская половина протокола — `web/src/views/viewSync.ts`.
- `internal/core` — provider-агностичные типы; `internal/provider` — `Provider`, опциональные
  интерфейсы (`TargetWatcher`), `Registry`.
- `internal/providers/kubernetes` — contexts из kubeconfig (`kubeconfig.go`), inotify-watch
  (`watch.go`), сессия (`session.go`), кэши informers (`cache.go`, `slim.go`, `listwatch.go`),
  вид поверх кэша (`view_watch.go`), kinds (`kinds.go`, `kind_*.go`), детали и связи
  (`resource.go`), метрики (`metrics.go`).
- Все ресурсы API (P8): каталог видов сессии (`catalog.go`: описанные `allKinds` + обнаруженные,
  ревизия, состояние, неподтверждённые группы; `provider.Cataloger`, API `ListKinds` →
  `KindsView`, `RefreshKinds`, событие `kinds_changed`), discovery v2/v1 (`discovery.go`),
  триггер — watch CRD (`crdwatch.go`), Table list/watch под Reflector-ом (`tablelw.go`), схема —
  эпоха на GVR (`schema.go`, `schema_store.go`: проба `limit=1` через `provider.ViewDescriber`,
  `Query.Schema`, поколение против устаревших проб, закреплённый обычный формат, перепроверка
  по отпечатку CRD и `F5`). Клиент — `web/src/views/useKinds.ts` (перечитывание каталога),
  `navGroups`/`NavSubgroup`/`CatalogNote` в `Workspace.tsx`. Известные встроенные ресурсы без
  своей проекции (Jobs, PVC, RBAC, HPA…) каталог кладёт в разделы навигации, как Lens
  (`placedKinds`/`navOrder` в `catalog.go`; решение пользователя 2026-09-30), в «API groups»
  остаются CRD и редкое; группа из подгрупп сворачивается целиком и свёрнута по умолчанию
  (`group:<имя>` в `navOpen`), `schema_changed`/`removed` в
  `viewSync.ts`.
- `internal/streams` — потоки для UI (логи): реестр одноразовых id с owner-ом сессии, NDJSON-
  писатель (коалесцирование, «толчок», heartbeat, дедлайн записи), обработчик с guard-ами
  (токен в пути, Host, Origin), loopback-сервер desktop, сохранение файлов (`save`).
  Клиентская половина — `web/src/logs/` (`useLogStream`, `ndjson`, `ansi`, `buffer`,
  `useLogFilter` + `search.worker`, `LogViewport`/`logSelection` из SPK-launcher; вкладки — `web/src/dock/`).
- Логи Kubernetes: `logs.go` (LogInfo, StreamLogs, наблюдение pod-а), `logs_fetch.go` (`pods/log`
  своим HTTP через транспорт client-go), `logs_reader.go` (ограниченный построчный читатель),
  `logs_source.go` (источник: курсор, инкарнации, решения на EOF), `logs_members.go` (живой набор
  pods группы), `logs_group.go` (агрегация, backlog-merge, лимиты); тесты — на фейковом kubelet-е
  `logs_fake_test.go`.
- Терминалы: `internal/streams/term.go` (мост WebSocket ↔ `ExecHandle.Run`: кредиты вывода
  `ack`, подтверждения ввода `iack`, resize «последнее значение», «вешание трубки» `^C ^D` при
  закрытии), `internal/api/exec.go` (открытие, прототипы для «Подключиться заново»),
  `internal/providers/kubernetes/exec.go` (снимок соединения `conn`, fallback WS → SPDY,
  `upgrade.go` — отменяемое рукопожатие). Клиент — `web/src/term/` (`protocol.ts`,
  `TerminalView.tsx` — ленивый xterm, `TerminalDialog.tsx`, `argv.ts`), вкладки — `web/src/dock/`.
- Туннели: `internal/forwards` (provider-агностичный менеджер: loopback-listener-ы, лимиты,
  поколения upstream-а, счётчики, `forwards_changed`), `internal/api/forwards.go`,
  `internal/providers/kubernetes/forward.go` (порты, выбор pod-а, закрепление UID) и
  `forward_dial.go` (WS-туннель / SPDY, потоки error+data, сторож живости, уход с мёртвого pod-а).
  Клиент — `web/src/tunnels/` (индикатор «⇄ N», панель, секция «Ports», диалог).
- Действия: `internal/api/actions.go` (`PrepareAction` из одной записи сессии, `RunAction`:
  строгая проверка запроса, сверка `ConfigRev`, выполнение на захваченной сессии),
  `internal/providers/kubernetes/actions.go` (матрица kind × действие, `Expect`, запись с
  предусловиями, повторы, классификация) и `actions_effects.go` (последствия по стратегии,
  политика PVC, контроллер pod-а, HPA, SSAR). Клиент — `web/src/actions/` (`Menu`,
  `ActionDialog`), меню строки и `Delete` — `ResourceTable` (`rowMenu`, `onDelete`), «Действия ▾»
  — `ResourceDrawer`, уведомление — `showNotice` в `store.ts`.
- Правка YAML (P9): `provider.Editor` (`EditSource`, `PrepareEdit`, `RunEdit`),
  `KindDescriptor.Editable`; `internal/api/edit.go` (база и грант — HMAC-конверты с
  `configRev`); Kubernetes — `edit_doc.go` (строгий разбор YAML 1.2 в `json.Number`, merge
  patch), `edit_patch.go` (патч от показанного текста к изменённому, скрытые пути, Secret —
  только `metadata`, столкновения, `editView` с точными десятичными), `edit_dryrun.go`
  (доказательство dry-run на маршрут), `edit.go` (просмотр: dry-run или локальное наложение,
  последствия, права; запись — один PATCH с uid+resourceVersion просмотра), `edit_errors.go`
  (`secretSafe`). Клиент — `web/src/edit/` (`diff.ts`, `EditDiff`, `EditDialog`, `guard.ts` +
  `DiscardPrompt`), редактор — `YamlView` (`editable`), кнопка/`E`/полоса — `ResourceDrawer`.
- Problems: `internal/providers/kubernetes/problems.go` — вид `problems` как набор обычных
  `viewWatch` (pods, workloads, services, ingresses, nodes, Warning events) через фильтрующие
  адаптеры `problemsFeed` в один sink (Reset источника → явные удаления), ID строки
  `<kindID>#<uid>`, `Ref` — сам объект (у события — Event); покрытие — `ViewStatus.Coverage`
  по источникам. Клиент — пункт навигации и заметка покрытия в `Workspace.tsx`; возможности
  строки (логи, exec, действия, `Delete`) — по `Ref.Kind` строки, не по виду таблицы.
- Палитра `Ctrl+K`: `web/src/palette/` (`score.ts` — свой fuzzy-скорер, `items.ts` — источники и
  грамматика `:`-команд, `store.ts`, `Palette.tsx`); алиасы видов — `KindDescriptor.Aliases`,
  scope/target — `TargetGroup.aliases` из опционального `provider.CommandAliaser` (k8s: `ns`,
  `ctx`); недавние объекты — `recent_objects` (миграция 0002, API `RecentObjects`/`TouchRecent`,
  запись деталями при успешном открытии). Запросы палитры странице (фильтр, открыть объект) —
  `PageReq{value, seq}`, применяются один раз при рендере.
- Клавиатура: `web/src/shortcuts.ts` — реестр `KEYS` (его показывает справка `?`,
  `HelpDialog.tsx`) и `globalShortcut` (один слушатель в `App`); области `F6` — атрибуты
  `data-area`/`data-area-focus`; возврат фокуса — `focusMark`/`restoreFocus`.
- Тексты провайдера — `core.Message{key, params, text}`: английский источник в
  `kubernetes/messages.go`, русский по ключу — `providerTexts` в `web/src/i18n.ts`
  (`messageText`, неизвестный ключ — английский текст).
- `internal/providers/compose` — Docker Compose: target-ы — Docker contexts (`contexts.go`, как
  docker CLI 29; inotify — `watch.go`), клиент Engine (`engine/`: транспорт unix/tcp/TLS, версия
  1.41–1.54 лениво, лимиты, классы ошибок, events, logs/stdcopy; ни одного повтора), фейковый
  Engine для тестов (`enginefake/`: модель объектов, events с историей 256, журнал логов с
  since/tail, hooks). Наблюдение: `feed.go` — лента на тип объекта (containers, networks,
  volumes, images) с эпохами (info → events since −1 с → list → inspect в пуле 8 → сверка
  грязных id; одна горутина на ленту), аренды и grace 60 с — `session.go` (`Open`, Scopes,
  Get со свежим inspect, Resync, Stats); вид — `view.go` (пересчёт строк из `World` по
  склеенному сигналу, разница с прошлым, статус — худший по лентам). Проекции — чистые функции
  над `World` (`world.go`): `kinds.go`, `rows.go` (идентичность, scope, Rev без лога проверок),
  `health.go` (таблица решения 6, сервис по приоритету), `resource.go` (факты, YAML inspect,
  связи uses/used-by/owns). Логи — `logs.go` (каналы stdout/stderr, члены сервиса из ленты,
  backlog-merge, продолжение по памяти прочитанных записей и якорю (`seenLog`/`replay`) —
  `since` Docker позиционный, ожидание старта остановленного, повтор с backoff после ошибки
  чтения при любом состоянии контейнера).
- `internal/providers/synthetic` — тестовый провайдер (`--test-api --test-synthetic`): логи,
  эхо-терминал и порты (`live.go`), переконфигурация (`POST /api/_test/synthetic/reconfigure`),
  вид Workloads с действиями (`actions.go`; `POST /api/_test/synthetic/controls` — права, отказ,
  `unknown`, задержка; `mutate` — чужое изменение/замена; `reset`).
- `internal/execshim` — shim для exec-плагинов kubeconfig (таймаут, смерть вместе с приложением).
- `internal/store` — SQLite, миграции `migrations/NNNN_*.sql`, `ui_prefs`, `target_state`,
  `recent_objects` (≤ 50 на target, ≤ 500 всего).
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
- Строка вида — ID = UID объекта: одноимённая замена = удаление + добавление; поздний delete
  старого UID не трогает новую строку. — `TestReplacementWithSameNameAndLateDelete`,
  `TestReplacementUnderSameNameDropsOldRow`.
- Ready вида — только после `HasSynced` регистрации обработчика (все начальные объекты дошли до
  вида), пустой список тоже; транспорт (list/watch) — отдельно: stale/error с классом. Запрет
  никогда не выглядит пустой таблицей. — `TestWatchDeliversSnapshotThenReadyThenChanges`,
  `TestEmptyListStillBecomesReady`, `TestForbiddenIsAnErrorNotAnEmptyTable`, kind-тесты RBAC.
- Всё, что проецирует объект и применяет его к виду (обработчики informer-а и перерасчёт
  health по таймеру), идёт под одним затвором вида `viewWatch.order` и читает store под ним;
  дедлайн принадлежит инкарнации (UID); таймер несёт поколение. —
  `TestDeadlineCannotResurrectADeletedObject`, `TestDeadlineSkipsAReplacementAndLateDeleteKeepsItsDeadline`.
- Виды принадлежат инкарнации сессии (уникальный owner); `Open`, начатый до `CloseOwner`,
  отклоняется (`revoked` по owner). — `TestOpenRacingCloseOwnerIsGone`,
  `TestViewsBelongToOneSessionIncarnation`.
- Транспорт: у LIST клиентский дедлайн, у watch — только на получение заголовков (здоровый
  поток не обрывать); поток, не восстановленный за 5 с, делает вид stale. —
  `TestWatchWithoutHeadersGoesStale`, `TestDroppedStreamThatStaysDownGoesStale`, `TestRenewedStreamStaysReady`.
- Строка несёт `Rev` (resourceVersion); открытые детали следят за своим объектом отдельным
  видом `Query.Name` и перечитываются по смене ревизии. — e2e «open details follow changes…».
- Метрики — только по UID объекта из кэша, существовавшего на момент сэмпла. —
  `TestMetricsSampleOlderThanTheObjectIsNotAttributed`.
- Поиск связей идёт по страницам (continue) до лимита после фильтра и сообщает усечение. —
  `TestRelationsFollowPagination`, `TestRelationsAreCappedAndSayIt`.
- Значения Secret/ConfigMap не попадают в списочные кэши (только имена ключей); в YAML
  деталей значения Secret — `<N bytes>`. — `TestConfigMapsAndSecretsKeepOnlyKeyNames`,
  `TestGetMasksSecretValues`, e2e «secrets never show their values».
- Все вызовы к кластеру — с контекстом и таймаутом; `rest.Config.Timeout` не ставить (рвёт watch);
  exec-плагины — только через `execshim.Wrap`. — `TestClientGoRequestWithHangingPluginFails`.
- Связи: владение — по UID контроллера (не только labels); Service без selector не «выбирает»
  все pods; ошибка поиска связей не роняет ресурс. — `TestDeploymentRelationsFollowControllerUID`,
  `TestSelectorlessServiceSelectsNothing`, `TestRelationErrorsDoNotFailTheResource`.
- Новый описанный kind: `kindDef` (колонки, `keep`-whitelist, `project` c health), регистрация в
  `allKinds`, строка таблицы-теста в `kinds_test.go`, строка в `kindOf` (resource.go);
  kind-тест `TestKindEveryKindBecomesReady` проверит его на кластере. Остальные ресурсы API
  показываются сами (P8) — описанный вид нужен только ради своей проекции и действий.
- Discovery — не доказательство исчезновения: ресурс удаляется из каталога только успешным
  ответом без него; неответившие группы сохраняют виды («не подтверждено»); вытесненный ответ
  только добавляет. Вид исчезнувшего ресурса кончается `removed` (окончательно, UI не
  переоткрывает); `gone` — по-прежнему «переоткрыть». Страница, чей вид сдался
  (`ViewState.halted`: `removed`, ошибка OpenView, исчерпаны повторы), получает новый вид —
  один раз на каждый листинг каталога, где её вид обслуживается, и при явной навигации к нему
  (клик, палитра); сессия каталога в ключ страницы не входит (хэш конфига включает AuthInfo —
  обновление токена сбрасывало бы фильтр). — `catalog_test.go`, `viewSync.test.ts`,
  `Catalog.test.tsx`.
- Никакого I/O в `Open`/`Kinds()`: discovery — фоновая single-flight задача сессии; обновление —
  по watch CRD (дебаунс 1 с, не позже 5 с), `F5` в навигации, resync. Периодических опросов нет.
- Колонки обнаруженного вида — только из ответа сервера (Table) и держатся эпохой: любой ответ
  с другими колонками кончает эпоху (`schema_changed`, UI переоткрывает, ограниченно); ответ
  пробы, начатой до инвалидации, не публикуется; признак «Table не обслуживается» (406,
  обычный список, ERROR 406 в потоке) закрепляет обычный формат до конца сессии. —
  `schema_session_test.go`, `tablelw_test.go`.
- Живой возраст у колонки Table — только при известном происхождении (точный встроенный
  GroupResource с «Age»; колонка CRD по позиции с путём `.metadata.creationTimestamp`);
  `description` — не доказательство. — `TestAgeProvenance*`.
- Действие над обнаруженным видом держит маршрут (GVR + scope) в `Expect` и на весь прогон
  (чтения, повторы после 409); финализаторы — в последствиях и в `Expect`. — `route_test.go`.
- Browser-режим отвечает только на loopback-`Host` (защита от DNS rebinding: `/` отдаёт токен). —
  `TestNonLoopbackHostIsRejected`.
- `/api/_test/*` — только с `--test-api` и токеном. — `TestTestAPIOnlyWithFlagAndToken`.
- Потоки к UI — только через `internal/streams` (loopback с токеном), никогда через `wails://`;
  поток регистрируется под блокировкой сессий и завершается с ней (`gone` терминален, без
  автопереоткрытия); reaper считает потоки использованием. — `TestLogStreamOfAClosedIncarnationIsGone`,
  `TestSessionClosingEndsItsLogStreamsGone`, `TestSessionWithOnlyALogTabIsNotReaped`.
- Origin/токен/метод проверяются до потребления id или записи файла; `save` требует Origin. —
  `TestRejectionsDoNotConsumeTheStream`, `TestSaveNeedsAnAllowedOriginAndWritesUnique`.
- Писатель потока после сброса, за которым тишина, шлёт `ping` через 100 мс (WebKitGTK иначе
  придерживает хвост пачки). — `TestQuietStreamIsNudgedThenHeartbeats`, спайк.
- Все слои логов ограничены по байтам и строкам (строка ≤ 256 КиБ, backlog вкладки 16 МиБ,
  UI ≤ 50k строк и 16 М символов на вкладку, ×1,2 пока пользователь читает; кадр ≤ 8 М), срабатывание
  лимита видно пользователю (счётчик вытеснения, маркер обрезанной строки, липкие gap/truncated). —
  `TestLineReaderBounds`, `buffer.test.ts`.
- Источник логов решает на EOF по **последнему** наблюдению pod-а (не ждёт «следующего события»),
  продолжение той же инкарнации — `sinceTime` (секунды) + пропуск повтора по счёту строк с
  проверкой меток; расхождение — `gap` (повтор лучше потери); новая инкарнация — с начала файла.
  — `TestLogResumeSkipsTheReplayExactly`, `TestLogRestartSeenBeforeEOFOpensAtOnce`,
  `TestLogFirstRequestBeforeTheCacheSynced`, kind `TestKindLogsFollowARestartingContainer`.
- Терминалы и туннели принадлежат приложению, а не сессии: переживают выбор другого target-а,
  reaper и пересоздание сессии из-за kubeconfig; хэндл несёт свой снимок соединения, «Подключиться
  заново» — тот же снимок и тот же pod. — `TestTerminalsOutliveTheirSession`,
  `TestTunnelsOutliveTheirSessionAndEndWithTheApp`, e2e «a terminal survives selecting another target».
- Хеш конфигурации target-а покрывает учётные данные — в UI уходит только `configRev` (HMAC с
  ключом процесса); по нему вкладка терминала и туннель помечаются «config changed». —
  `TestTerminalsOutliveTheirSession` (JSON без хеша), e2e «…opened before a reconfiguration say so».
- Закрытие терминала «вешает трубку» in-band (`^C`, через 100 мс `^D`), отмена — только если
  команда не кончилась за 2 с: containerd не убивает процессы exec при разрыве. —
  `TestTermClosingThePageHangsUpInBand`, kind `TestKindTerminalClosingTheTabEndsTheShellAndItsChild`.
- Вывод терминала — только по кредиту страницы (окно 1 МиБ, `ack`), ввод — окно 256 КиБ (`iack`);
  читатель WS не блокируется никогда. — `TestTermOutputStopsAtTheWindowUntilAcked`,
  `TestTermCommandNotReadingStdinKeepsControlAlive`.
- У stdin терминала один писатель (`stdinLoop`): очередь ввода, `^C` по `intr`, при закрытии —
  сброс очереди, затем `^C`/`^D`. Ctrl+C страницы — управляющее `intr` вне окна ввода: сервер
  сбрасывает свою очередь (засчитывая в `iack`) и пишет `^C` первым; неверный `iack` — нарушение
  протокола. — `TestTermClosingDropsQueuedInputAndHangsUpAfterIt`,
  `TestTermInterruptDropsQueuedInputAndGoesFirst`, `protocol.test.ts`.
- Запуски терминала принадлежат ему в реестре потоков (owner `term:<id>`): `ForgetTerminal`
  завершает подключённый и отзывает неподключённый; «Подключиться заново» и «забыть» атомарны. —
  `TestForgettingATerminalEndsItsRuns`, `TestAReopenRacingForgetLeavesNoRun`.
- `Close` провайдерских хэндлов — никогда под блокировками API: под своим мьютексом регистрировать
  через `Registry.Register`/`RegisterTerm` и вызывать `release` после разблокировки. Сброс ввода
  по `intr` вычитает только снятое с очереди (пишущийся кусок остаётся в окне). —
  `TestARefusedRunIsClosedOutsideThePrototypesLock`, `TestRegisteringLeavesClosingToRelease`,
  `TestTermInterruptKeepsTheChunkBeingWrittenCounted`.
- Вставка из буфера (асинхронная) идёт только в соединение, в котором был жест; иначе видимый
  отказ. Живость в `TerminalView` — локальная для монтирования (StrictMode монтирует дважды). —
  `TerminalView.test.tsx`.
- Exec: явный `Instance` workload-а сверяется с цепочкой контроллеров (UID); годный pod — не
  удаляется и Running|Pending, канал — по своему состоянию; полный листинг сверх 20 000 —
  ошибка, не обрезка. — `TestAnExplicitInstanceMustBelongToTheWorkload`,
  `TestARunningInitContainerOfAPendingPodCanBeOpened`.
- Отказ апгрейда (SPDY) читается под отменой и дедлайном 5 с, соединение не переиспользуется. —
  `TestARefusalWithAStalledBodyEndsByCancelOrDeadline`.
- Ошибка одного соединения туннеля не закрывает общий upstream; после ошибки upstream сверяет
  свой pod (одна проверка на все одновременные сбои, живёт не дольше upstream-а — Stop её не ждёт)
  и уходит, если pod исчез; молчащий error stream через 5 с — ошибка. —
  `TestPortErrorFailsOneConnectionAndTheNeighbourLives`, `TestAServiceTunnelMovesOnWhenItsPodIsDeleted`,
  `TestClosingTheUpstreamCancelsTheFailureCheck`, `TestASilentErrorStreamIsATimeout`.
- Номер порта может быть объявлен для TCP и UDP (DNS 53): туннель берёт TCP-запись. —
  `TestATCPPortSharingItsNumberWithUDPIsAccepted`.
- Туннель слушает только loopback, показывает фактические адреса (не `localhost`); явный занятый
  порт — `conflict`, авто — удалённый при ≥ 1024 и свободном, иначе любой. —
  `TestExplicitPortInUseIsAConflict`, `TestAutoPortTakesTheRemoteOneOrAnyFree`, e2e.
- Первый `Connect` туннеля синхронный: неудача откатывает туннель и видна в диалоге. —
  `TestFailedFirstConnectRollsBack`.
- Вывод терминала — недоверенные данные: без OSC 52, без открытия ссылок, без смены заголовков.
- Regex-поиск в UI — только в Worker с бюджетом времени; plain и фильтр `*` — линейные. —
  `match.test.ts` (`(a|aa)+$` убивает воркер).
- Действие выполняется над подтверждённым объектом и планом: `RunAction` требует UID,
  `ConfigRev` и `Expect`, читает строго по UID (заменённый одноимённый — `gone`), несовпавший
  `Expect` — `conflict` без записи; запись — с предусловиями UID + `resourceVersion`. —
  `internal/api/actions_test.go`, `TestKindActionOnAnObjectReplacedBetweenReadAndWrite`,
  `TestKindActionReplicasChangedAfterThePlanIsAConflict`.
- Повтор записи — только при отказе предусловия (409 — не 422: провал JSON Patch `test` не
  отличим от отказа валидации, поэтому scale — merge patch `/scale` с uid+RV) и только
  когда перечитывание доказало, что записи не было (тот же UID и `Expect`, другая версия), ≤ 3.
  Запись действия — ровно один HTTP-запрос: `restWriter` (`actions_writer.go`) с
  `MaxRetries(0)`, не dynamic client (client-go сам переотправляет ответы 5xx/429 с
  `Retry-After`, и применённый restart ушёл бы дважды). Неоднозначные ответы (5xx, таймауты,
  шлюз) и ответ без HTTP-статуса — `unknown`, без повторов; в UI транспортный сбой `RunAction`
  (обрыв, некодированный HTTP-ответ, ошибка рантайма Wails) — тоже «результат неизвестен». —
  `TestKindActionStatusChurnIsRetried`, `actions_run_test.go`, `actions_wire_test.go`,
  `ActionDialog.test.tsx`, `client.test.ts`.
- `Expect` связывает всё, что читают последствия плана (у scale StatefulSet — число
  `volumeClaimTemplates`, `whenScaled`, `whenDeleted`, `ordinals.start`): изменившийся после плана
  вход — `conflict`, а не выполнение под устаревшим обещанием. — `actions_prepare_test.go`.
- Последствия — только известное, «запрошено»/«может», по стратегии; судьба данных PVC — по
  reclaim policy тома; pod-ы удаляемого workload-а считаются по цепочке контроллеров (у
  Deployment — через его ReplicaSet-ы), не по селектору, без уже удаляемых; права «не удалось
  проверить» (ошибка SSAR или `evaluationError` без `denied`) ≠ «разрешено»; опоздавшие к дедлайну
  проверки не ждутся (права — «неизвестно», HPA — предупреждение, счёт pod-ов опускается). —
  `actions_prepare_test.go`, `TestKindActionDeleteOfADeploymentCountsThePodsItOwns`,
  `TestKindActionStatefulSetClaimsFollowTheRetentionPolicy`, `ActionDialog.test.tsx`.
- Подтверждение (все contexts RW — это только UX): видны context, сервер, namespace, вид, имя;
  опасный план — красная кнопка и фокус на «Отмена»; удержанный Enter не подтверждает; Enter в
  поле числа — просмотр, не выполнение; один `RunAction` на подтверждение (синхронный замок);
  Esc не закрывает во время выполнения; цель меню — строка под курсором / текущий объект деталей;
  `Delete` — только в теле таблицы. — `ActionDialog.test.tsx`, `WorkspaceActions.test.tsx`,
  `tests/e2e/actions.spec.ts`.
- Правка YAML пишет только просмотренное: `RunEdit` заново выводит патч из original+edited,
  сверяет хэш с грантом и пишет один раз с uid+resourceVersion просмотра (без перечитывания и
  повторов); `PATCH` с `dryRun` — только по доказанному маршруту (точный GVR описанного вида,
  локальный APIService, `dryRun` в OpenAPI PATCH), иначе локальное наложение без единого
  изменяющего запроса, план опасный; просмотр ограничен сроком и кончается с сессией. —
  `edit_session_test.go`, `edit_dryrun_test.go`, `TestKindEdit*`.
- Правка Secret — только `metadata`; строки сервера об объекте Secret (message, reason,
  cause) не показываются никогда — только известные значения перечислений своими словами. —
  `TestSecretRefusalsNeverPrintServerStrings`, `TestASecretsReadRefusalsAreHidden`.
- Несохранённые правки не теряются молча: всё, что уводит от редактора (закрытие деталей, Esc,
  связи, вкладка, другой объект, вид, scope, target, палитра), идёт через `mayLeave`
  (`web/src/edit/guard.ts`) — «Отбросить правки?» с фокусом на «Продолжить правку». —
  `WorkspaceEdit.test.tsx`, `guard.test.ts`, e2e-kind «edit a ConfigMap».
- Версии `github.com/wailsapp/wails/v3` и `@wailsio/runtime` совпадают (сейчас `3.0.0-beta.26`).
- `go build ./...` без тега `wails` обязан проходить: desktop-код за тегом.
- Стартовый JS-чанк < 300 КБ gz (сейчас вход 86 КБ + общие ~18 КБ), xterm (~87 КБ gz)/CodeMirror — только ленивые чанки. — `web/scripts/check-bundle.mjs`
  (часть `pnpm build`).
- Wails `LogLevel` — Warn: Info логирует каждый asset-запрос, Debug — результаты биндингов.
- Горячие клавиши — по `KeyboardEvent.code` (`web/src/keyboard.ts`), иначе не работают в русской
  раскладке; все глобальные — в реестре `web/src/shortcuts.ts` (справка `?` показывает его же).
  Терминал получает все клавиши (и `Ctrl+K`, Esc, `F6`); в полях ввода работают только `Ctrl+K`,
  Esc и `F6` (F-клавиши не печатают — иначе из фильтра не уйти); `?` и `/` — по физической
  клавише или по символу. — `shortcuts.test.ts`, `Keyboard.test.tsx`, `tests/e2e/keyboard.spec.ts`.
- Problems — «проблемы в наблюдаемой области»: состояние (не подтверждённое свидетельством) не
  показывается как сбой; недавний рестарт — по `lastState.terminated.finishedAt` (10 мин),
  Warning event — одна строка на UID с окном 15 мин по последнему наблюдению, истекают
  дедлайнами; «0 проблем» при неполном покрытии перечисляет непокрытое. —
  `problems_test.go`, `TestKindProblems*`, `Problems.test.tsx`, `tests/e2e/problems.spec.ts`.
- Палитра не делает LIST ради поиска: объекты — только текущая таблица и недавние. Команда
  выбирается (курсор) только при одном точном совпадении; частичное/неоднозначное — список без
  курсора, Enter ничего не делает; тихого «первого fuzzy-кандидата» у команд нет. Недавние —
  с UID в идентичности: заменённый одноимённый открывается как «объекта больше нет». —
  `items.test.ts`, `Palette.test.tsx`, `internal/store` тесты, `tests/e2e/palette.spec.ts`.
- `ListScopes` никогда не возвращает `null` (пустой срез), UI терпит и `null`. — e2e palette.
- Фразы провайдера (последствия, предупреждения, причины) — ключ + параметры, не готовый текст;
  новый ключ = английский в `messages.go` + русский в `providerTexts`. —
  `TestTheUIsTranslationsCoverEveryMessage` (разбирает `i18n.ts`).
- Поздний ответ `RunAction` (после клиентского таймаута) меняет только «неизвестно» своего
  прогона (номер прогона отдельно от поколения диалога) или становится уведомлением с target-ом;
  ничего не отправляет заново и не трогает более новый диалог. — `ActionDialog.test.tsx`.
- Наблюдение Docker — best effort, не watch Kubernetes: событие — только подсказка, объект
  меняет лишь inspect (404 = удалён), старое `destroy` не удаляет новую инкарнацию; ответы
  старой эпохи не попадают в новую (одна горутина на ленту, конец потока событий обрывает
  эпоху сразу); Ready — после сверки и при живом потоке; запрет events — error, не пустой
  Ready; зависший inspect — stale с объяснением; пропущенную демоном доставку чинит только
  «Перечитать» (`Resyncer`, строки не исчезают до успешного снимка). Никаких периодических
  relist-ов. — `feed_test.go` (`TestFeed*`), dind `TestDindDaemonStopStaleRecovery`.
- Идентичность Compose: контейнер — полный id (имя — `Ref.Title`), сервис — `project/service`,
  том — `name@CreatedAt` (пересоздание в ту же секунду не различимо — сказано в деталях), образ
  — id; `Rev` без `Health.Log`/`FailingStreak`. — `rows_test.go`, `resource_test.go`.
- Логи Docker: журнал один на все перезапуски — «Предыдущий» не предлагается; продолжение —
  позиционный `since` + сверка с прочитанными записями до якоря (см. Things that bite), незнакомая
  запись до якоря — `gap` (повтор лучше потери); ошибка чтения остаётся видимой и повторяется
  с backoff, «ended» её не затирает; остановленный контейнер
  ждёт старта в ленте, не опросом. — `logs_test.go` (`TestLogs*`), dind
  `TestDindServiceLogsThroughRestart`.
- Каталог данных — `~/.spk/ocular` (`SPK_OCULAR_HOME`); временные файлы агентов — в
  `.agents/tmp` solution, не в `/tmp`.

## Things that bite

- **kubeconfig — YAML 1.1**: голые `y`/`n`/`on`/`off`/`yes`/`no` — булевы, context с таким именем
  без кавычек роняет разбор всего файла (`cannot unmarshal bool into … name of type string`). В
  фикстурах имена всегда в кавычках.
- **Playwright выполняет `playwright.config.ts` в раннере И в каждом воркере**: `mkdtemp` в конфиге
  без защиты даёт воркерам другой каталог, чем у webServer. Каталог создаётся один раз и передаётся
  через `process.env.E2E_ROOT` (воркеры наследуют env).
- **client-go v0.37: WatchList включён по умолчанию** — начальное состояние приходит watch-потоком
  (`sendInitialEvents`), а не List; поэтому «успешный List» не может быть признаком ready, а обёртка
  `ListerWatcher` обязана сохранять `ListOptions` и сообщать `IsWatchListSemanticsUnSupported`
  (fake-клиенты в тестах — `watchList=false`). Сервер без WatchList отвечает на
  `sendInitialEvents` 422 («…forbidden for watch unless the WatchList feature gate is enabled»),
  reflector сам уходит на LIST — это не сбой транспорта (иначе вид на миг показывал «Cannot
  show»); сессия запоминает отказ и больше не спрашивает (`cacheManager.noWatchList`). —
  `TestAServerWithoutWatchListIsNoErrorAndAskedOnce`. Нашёл пользователь на своём кластере.
- **Exec-плагин kubeconfig без контекста** (client-go `exec.go`): зависший `yc`/`kubelogin` вешает
  все запросы кластера и остаётся сиротой — только через shim (`docs/spikes/2026-09-29-exec-plugin-hang.md`).
- **`metav1.Time` — точность до секунды**: время, прошедшее через ObjectMeta (slim-кэш), теряет доли
  секунды; тесты на пороги времени держат запас > 1 с.
- **Trimmed Unstructured дорог по памяти** из-за накладных расходов `map[string]any` (~6 КБ/pod
  после фильтра) — в кэше `slimObject` (~1,3 КБ); интернирование строк почти не помогает.
- **WebKitGTK не показывает `:focus-visible` для фокуса, поставленного скриптом** (начальный фокус
  диалога, ловушка Tab) — у кнопок диалога действий явное кольцо по `:focus`, иначе не видно,
  что нажмёт Enter.
- **Выпадающий список `<select>` в WebKitGTK — системный GTK-попап**: пункты рисуются шрифтом
  и размером GTK, CSS на них не действует (кнопку саму стилизует глобальное правило `select` в
  `index.css`). Выбор scope — свой `ScopeSelect` (`web/src/components/ScopeSelect.tsx`, поиск,
  стрелки, Enter); в e2e — `pickScope` из `fixtures.ts`. Нашёл пользователь.
- **CodeMirror рисует только видимые строки**: `toContainText` по `.cm-editor` не видит текст
  ниже окна — e2e прокручивает `.cm-scroller` (dind «inspect YAML» после увеличения шрифтов).
- **Неявная отправка формы**: Enter в поле не отправляет форму, если её кнопка по умолчанию
  `disabled` — поэтому «Просмотреть» не отключается на время подготовки.
- **Фейковый dynamic client**: reactor-ы вызываются под его блокировкой — менять объекты только
  через `c.Tracker()`, вызов клиента из reactor-а — дедлок; «зависший» reactor блокирует и все
  остальные вызовы, поэтому зависание моделируется обёрткой `dynamic.Interface` вне fake
  (`stallingDyn` в `actions_prepare_test.go`).
- **client-go переотправляет запрос**, получивший 5xx/429 с `Retry-After` (`rest.Request`,
  по умолчанию до 10 раз) — для неидемпотентной записи это второе применение; запись действий —
  через REST-клиент с `MaxRetries(0)`.
- **После `kubectl rollout status` pod-ы старого ReplicaSet ещё Terminating** — всё, что считает
  pod-ы «сейчас», пропускает объекты с `deletionTimestamp`.
- **PVC, оставленные StatefulSet-ом при scale down** (`whenScaled=Retain`), сохраняют
  ownerReference на него и удаляются вместе с ним при `whenDeleted=Delete` (проверено на kind).
- **kubelet отвечает 200 с текстом «unable to retrieve container logs for containerd://…»**, если
  предыдущий контейнер crashloop-а подменили во время чтения `previous` — e2e перечитывает.
- **client-go считает watch короче секунды без событий ошибкой** («very short watch») и уходит в
  backoff — тестовые серверы должны держать поток > 1 с.
- **dynamic fake фильтрует по label selector и ответы reactor-а** — объекты в reactor-ах должны
  нести нужные labels.
- **ClientGo `ListAction`** не отдаёт `ListOptions` — в reactor-е приводить к `ListActionImpl`.
- **Scale-down в e2e**: pod показывается Terminating до окончания grace-периода, строка исчезает
  позже — считать «живые» строки и ждать удаления с запасом.
- **dynamic fake**: не соблюдает field selectors и не сопоставляет PodMetrics с ресурсом `pods`
  группы metrics.k8s.io — такие вещи проверять на kind или отдавать через reactor.
- **klog client-go** пишет каждую неудачную попытку watch в stderr — заглушен (`SPK_OCULAR_KLOG=1`).
- **HTTPS_PROXY в окружении** уводит запросы к фикстурным (несуществующим) кластерам в прокси —
  e2e сбрасывает прокси-переменные в env webServer.
- **Порты dev-серверов заняты чужими процессами** (другие сессии/worktree): перед запуском проверять
  `ss -ltn "sport = :PORT"` и брать свободный, а не убивать чужой процесс.
- **jsdom без раскладки**: `getBoundingClientRect` = 0 → виртуальная таблица пустая; в
  `web/vitest.setup.ts` элементам задан размер экрана.
- **Язык UI**: gettext-порядок `LANGUAGE` > `LC_ALL` > `LC_MESSAGES` > `LANG`; у пользователя
  `LANGUAGE=en_US`, поэтому `LANG=ru_RU…` один не даёт русский UI. `LC_ALL=C` — английский.
- **jsdom** не знает `scrollIntoView` — заглушка в `web/vitest.setup.ts`; `@wailsio/runtime` там же
  замокан (побочные эффекты при импорте).
- **Мёртвая/зависшая D-Bus**: GLib подключается без таймаута и окно не появляется. Probe 2 с и
  подмена `DBUS_SESSION_BUS_ADDRESS` (`internal/desktop/busprobe_linux.go`); проверено: окно за
  0,2 с при недоступной шине.
- **WebKitGTK придерживает хвост пачки** стрима (20–60 КБ), пока не придут новые байты — и на
  loopback HTTP, недетерминированно (~1 из 4). Лечится маленьким кадром через 100 мс.
- **Origin desktop-страницы — `wails://localhost`** (не `wails://wails`); fetch к loopback без
  точного `Access-Control-Allow-Origin` — `TypeError: Load failed`.
- **`pods/log`**: `sinceTime` с долями секунды обрезается до секунды; `limitBytes` режет посреди
  строки и считается от начала окна tail (не гарантирует свежие строки); `tailLines=0` — «ничего»;
  метка — одна на логическую строку (продолжения частичных CRI-записей без неё).
- **Числа в slim-кэше — `float64`** (JSON): `unstructured.NestedInt64` вернёт 0 — читать через `i64`.
- **LIST разрешён, WATCH нет**: informer «синхронизируется» списком, но изменений не будет — судить
  о здоровье кэша по транспорту, а не только по `HasSynced`.
- **Логи группы**: тесты на kind меняют число реплик `chatter` — выбирать самый старый pod
  (scale down удаляет новые).
- **Hook окружения блокирует `pkill -f`/`pgrep -f` с шаблоном из той же команды** — останавливать
  фоновые процессы по PID-файлу или через TaskStop.
- **containerd не завершает процессы exec при разрыве соединения** (kind, измерено): отмена exec
  оставляет shell и его foreground-процесс; закрытие stdin до TTY как EOF не доходит — отсюда
  «вешание трубки» мостом. Убитое `SIGKILL`-ом приложение (или `kubectl exec`) оставляет shell-ы
  в pod-е; Playwright по умолчанию гасит webServer именно так — в конфигах `gracefulShutdown: SIGTERM`.
- **`TMPDIR` с `..` в пути** роняет `TestSaveNeedsAnAllowedOriginAndWritesUnique` (сравнение
  путей): задавать канонический абсолютный путь.
- **Предикат фолбэка exec на SPDY**: client-go v0.37 возвращает `UpgradeFailureError` из
  `k8s.io/streaming/pkg/httpstream`; одноимённый предикат устаревшего `apimachinery/pkg/util/httpstream`
  его не узнаёт (фолбэк не сработал бы никогда).
- **kubelet держит port-forward соединение удалённого pod-а**: соединение живо, каждый поток
  получает ошибку («failed to find sandbox») — без проверки pod-а туннель к Service не переехал бы.
- **Пинги spdystream без таймаута** (неудача только в лог): полуоткрытое соединение выглядело бы
  живым — свой сторож чтения 3 × 10 с.
- **Рукопожатия WS (gorilla) и SPDY client-go не отменяются контекстом** — свой `upgrade.go`.
- **busybox ash глотает ввод сразу после приглашения** (гонка с его запросом позиции курсора
  `ESC[6n`; `kubectl exec -it` теряет так же): e2e на kind сначала «успокаивает» shell пустыми
  строками.
- **xterm.js строит Ctrl+буква из `keyCode`, а WebKitGTK у кириллической клавиши его не даёт**:
  в русской раскладке Ctrl+C не прерывал, Ctrl+K/Ctrl+[ не доходили. Терминал берёт
  управляющий символ по физической клавише (`code`), когда `key` не ASCII
  (`TerminalView.tsx`, `layoutControl`); латиница — как у xterm. Нашла desktop-проверка P5.
- **Xvfb сбрасывает раскладку, когда отключается последний клиент** — `setxkbmap` делать, когда
  окно приложения уже открыто.
- **Chromium не засчитывает переполнение grid-треков абсолютных строк целиком** в прокрутку
  контейнера: у строк/заголовка таблицы явный `min-width` (сумма минимальных ширин колонок).
- **React compiler lint запрещает `setState` в эффектах** — «применить запрос один раз» делается
  при рендере по смене `seq` (`PageReq`), а обычная навигация его сбрасывает, иначе он повторится
  при повторном монтировании.
- **user-event: `{?}`/`{/}` дают `code: Unknown`** — клавиши по символу проверять и по `key`
  (поэтому `?` и `/` сопоставляются по коду или по символу).
- **`pnpm exec tsc -b` переписывает отслеживаемый `web/tsconfig.tsbuildinfo`** — перед коммитом
  `git checkout web/tsconfig.tsbuildinfo`.
- **`rest.Result.Raw()` не разбирает `Status`**: ошибка из `Raw()` — голая «the server
  rejected our request…» без message и causes; разбор делает только `Result.Error()` —
  брать ошибку из него, тело — из `Raw()`. Нашёл kind-тест P9.
- **`sigs.k8s.io/yaml.Marshal` читает числа обратно через float64** (JSON → YAML):
  `json.Number("9007199254740993.0")` выходит как `9.007199254740992e+15`. Точный вывод —
  `exactDecimals` в `edit_patch.go`. kube-apiserver сам хранит нецелый JSON-литерал как float64
  (dry-run на kind: …992), целые — точно.
- **Две сетки `resources`**: список событий в деталях — тоже `ResourceTable`; в e2e брать `.first()`.
- **Фильтры `/events` Moby складываются по И между ключами**: `type=[container,network]` с
  `label=com.docker.compose.project` отбросил бы события сети (`connect` называет контейнер
  только в атрибутах). Лента контейнеров фильтрует compose-label на месте. `event=health_status`
  совпадает с `health_status: healthy` (Action режется по `:`); у контейнерных событий labels —
  в `Actor.Attributes`.
- **`since` логов Docker позиционный** (Docker 29, измерено на dind): находит первую строку
  журнала с меткой ≥ since и отдаёт всё после неё — в том числе строки другого потока с меткой
  чуть раньше (stdout и stderr штампуются раздельно, в журнале метки идут не монотонно).
  Первая строка ответа может стоять в журнале раньше доставленной (другой поток, строка до
  окна tail), поэтому ни метка, ни счёт не доказывают, где повтор: продолжение помнит последние прочитанные записи (≤ 512: метка, поток, длина и хэш текста) и якорь — последняя
  прочитанная строка с меткой; продолжение — `since=<метка якоря>` (позиционный `since` точно
  вернёт якорь) или раньше — с начала строки, чтение которой оборвалось; известные записи до
  якоря отбрасываются, незнакомая — доставляется с `gap` (старая строка вне окна tail, ротация);
  строка, продолжившая доставленную без `\n` (контейнер остановился посреди строки), — только
  новой частью.
  Нашёл dind `TestDindServiceLogsThroughRestart`, углубило ревью Codex (фейк моделирует
  позиционный `since`, частичные записи, частичную ротацию).
- **dind после `docker stop/start` не поднимается**, если pid-файлы dockerd/containerd пережили
  перезапуск (pid переиспользован: «process with PID 40 is still running») — у `ocular-dind`
  `/run` на tmpfs (`dind-up.sh` пересоздаёт старый контейнер без него).
- **Table-ответ сервера**: заголовки колонок — только в первом событии watch-потока (любого
  типа, BOOKMARK тоже); `date`-ячейка CRD — уже текст длительности («40s»), метку не
  восстановить; объект строки — полный объект ресурса (служебные ячейки — отдельно, не в нём).
- **Имена printer columns CRD не уникальны** — сопоставлять по позиции (сервер: Name, затем
  колонки по порядку; без колонок — Age).
- **`t.Context()` уже отменён в `t.Cleanup`** — ожидания в cleanup-ах — с `context.Background()`.
- Кандидат из соседей, ещё не встреченный здесь: fetch с `Blob`/`FormData`-телом через `wails://`
  роняет WebKitGTK (сохранение логов в desktop — строковым телом на loopback).
