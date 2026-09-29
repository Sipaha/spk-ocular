# spk-ocular — гид для агентов

Лёгкий локальный просмотрщик инфраструктуры: «Lens-like visibility + kubectl-like простота,
без тяжеловесности Lens». Первый provider — Kubernetes, следующий — Docker Compose.
Go + Wails v3 + React. Спецификация: `docs/specs/2026-09-29-spk-ocular-design.md`.
Планы: `docs/plans/`. Бэклог: `docs/backlog.md`.

Статус: P0 (каркас) и P1 (ресурсы, детали, метрики) готовы — `docs/plans/`. Следующий — P2 (логи).

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
- Реальный кластер (kind в Docker; `KIND=<путь>`, если kind не в PATH): `make kind-up` (kubeconfig —
  `build/kind-ocular-dev.kubeconfig`, никогда не в `~/.kube`), `make test-kind` (Go-тесты `*Kind*`),
  `make e2e-kind` (Playwright), `make kind-down`. Обе цели **падают**, если кластера нет, и сами
  сидируют фикстуры (`scripts/kind-seed.sh`, `kind-rbac.sh`, `kind-metrics.sh`). Нагрузка —
  `scripts/kind-load.sh <kubeconfig> [up|down]`, замер — `node tests/e2e/measure-kind.mjs <bin>
  <kind kubeconfig> <viewer kubeconfig> <scratch>` (печатает тайминги, счётчики `/api/_test/stats`
  и Private_Dirty). Синтетика памяти кэша: `OCULAR_SYNTH=1 go test -run Synthetic -v ./internal/providers/kubernetes/`.
- `SPK_OCULAR_KLOG=1` — вернуть логи client-go (klog) в stderr для отладки.
- `E2E_BIN=<путь> E2E_PORT=<порт>` — e2e против другой browser-сборки.
- Проверка desktop без экрана пользователя: `xvfb-run -a -s "-screen 0 1400x900x24" <скрипт>`,
  окно ищется `xwininfo -root -tree | grep '"SPK Ocular"'`, снимок — `import -window root`.

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
- Destructive-действия — только с подтверждением, в котором виден context/namespace/объект.
- Версии `github.com/wailsapp/wails/v3` и `@wailsio/runtime` совпадают (сейчас `3.0.0-beta.26`).
- `go build ./...` без тега `wails` обязан проходить: desktop-код за тегом.
- Стартовый JS-чанк < 300 КБ gz (сейчас ~94 КБ), xterm/CodeMirror — только ленивые чанки. — `web/scripts/check-bundle.mjs`
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
- Кандидаты из соседей, ещё не встреченные здесь (spk-mm-client AGENTS.md): fetch с `Blob`/
  `FormData`-телом через `wails://` роняет WebKitGTK; скрытое окно WebKitGTK копит rAF — потоки
  (логи, exec) пойдут через loopback-сервер с токеном, не через `wails://`.
