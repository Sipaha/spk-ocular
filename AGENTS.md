# spk-ocular — гид для агентов

Лёгкий локальный просмотрщик инфраструктуры: «Lens-like visibility + kubectl-like простота,
без тяжеловесности Lens». Первый provider — Kubernetes, следующий — Docker Compose.
Go + Wails v3 + React. Спецификация: `docs/specs/2026-09-29-spk-ocular-design.md`.
Планы: `docs/plans/`. Бэклог: `docs/backlog.md`.

Статус: P0 (каркас), P1 (ресурсы, детали, метрики), P2 (логи) и P3 (терминалы, туннели) готовы —
`docs/plans/`. Следующий — P4 (действия restart/scale/delete).

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
- `internal/providers/synthetic` — тестовый провайдер (`--test-api --test-synthetic`): логи,
  эхо-терминал и порты (`live.go`), переконфигурация (`POST /api/_test/synthetic/reconfigure`).
- `internal/execshim` — shim для exec-плагинов kubeconfig (таймаут, смерть вместе с приложением).
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
- Новый kind: `kindDef` (колонки, `keep`-whitelist, `project` c health), регистрация в
  `allKinds`, строка таблицы-теста в `kinds_test.go`, строка в `kindOf` (resource.go);
  kind-тест `TestKindEveryKindBecomesReady` проверит его на кластере.
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
- Destructive-действия — только с подтверждением, в котором виден context/namespace/объект.
- Версии `github.com/wailsapp/wails/v3` и `@wailsio/runtime` совпадают (сейчас `3.0.0-beta.26`).
- `go build ./...` без тега `wails` обязан проходить: desktop-код за тегом.
- Стартовый JS-чанк < 300 КБ gz (сейчас вход 86 КБ + общие ~18 КБ), xterm (~87 КБ gz)/CodeMirror — только ленивые чанки. — `web/scripts/check-bundle.mjs`
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
- **client-go v0.37: WatchList включён по умолчанию** — начальное состояние приходит watch-потоком
  (`sendInitialEvents`), а не List; поэтому «успешный List» не может быть признаком ready, а обёртка
  `ListerWatcher` обязана сохранять `ListOptions` и сообщать `IsWatchListSemanticsUnSupported`
  (fake-клиенты в тестах — `watchList=false`).
- **Exec-плагин kubeconfig без контекста** (client-go `exec.go`): зависший `yc`/`kubelogin` вешает
  все запросы кластера и остаётся сиротой — только через shim (`docs/spikes/2026-09-29-exec-plugin-hang.md`).
- **`metav1.Time` — точность до секунды**: время, прошедшее через ObjectMeta (slim-кэш), теряет доли
  секунды; тесты на пороги времени держат запас > 1 с.
- **Trimmed Unstructured дорог по памяти** из-за накладных расходов `map[string]any` (~6 КБ/pod
  после фильтра) — в кэше `slimObject` (~1,3 КБ); интернирование строк почти не помогает.
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
- **Xvfb сбрасывает раскладку, когда отключается последний клиент** — `setxkbmap` делать, когда
  окно приложения уже открыто.
- **Две сетки `resources`**: список событий в деталях — тоже `ResourceTable`; в e2e брать `.first()`.
- Кандидат из соседей, ещё не встреченный здесь: fetch с `Blob`/`FormData`-телом через `wails://`
  роняет WebKitGTK (сохранение логов в desktop — строковым телом на loopback).
