# P1 — ресурсы Kubernetes: живые таблицы, детали, метрики

> Для агентов: задачи выполняются по порядку, шаги отмечаются `- [x]`; каждая задача —
> отдельные коммиты `area: summary`, `make check` зелёный перед коммитом.

**Цель:** выбрав context, пользователь видит namespaces и 10 видов ресурсов (Pods,
Deployments, StatefulSets, DaemonSets, Services, Ingresses, ConfigMaps, Secrets, Nodes,
Events) в живых таблицах с health и CPU/RAM; открывает объект — поля, YAML, связанные
Events, связи (владельцы вверх, pods вниз) с переходом по ним.

**Архитектура:** контракты — спецификация, разделы «Живые таблицы», «Provider API»,
«Ключевые технические решения» (уточнены ревью Codex 2026-09-29). Движок видов
(`internal/views`) provider-агностичен; Kubernetes-адаптер отдаёт ему поток `Delta` из
ленивых informers.

**Спецификация:** `docs/specs/2026-09-29-spk-ocular-design.md`.

## Глобальные ограничения

- Всё из P0 (AGENTS.md «Правила»).
- `internal/core`, `internal/views`, общий UI не знают про pod/namespace/container.
- Версия вида — счётчик Ocular, не `resourceVersion`; `viewId` не переиспользуется.
- Запрет (403) никогда не выглядит как пустая таблица.
- Значения Secret/ConfigMap не хранятся в списочных кэшах; в YAML Secret значения маскируются.
- Никаких опросов кластера, кроме метрик для видимой таблицы (≥ 15 с).
- Informers: resync 0, остановленный — не перезапускается, только пересоздаётся.

## Решения P1 (агент, 2026-09-29)

- **Тестовый кластер** — kind `ocular-dev` (бинарь `../.agents/tools/kind`, kubeconfig
  `../.agents/tmp/kind-ocular.kubeconfig`, не в `~/.kube` пользователя). Реальные кластеры
  пользователя — только чтение и только с его разрешения.
- **События объекта** — это вид kind `events` с field selector `involvedObject.uid=<uid>`:
  тот же механизм, без отдельного API.
- **Метрики** — `metrics.k8s.io` через dynamic client (без модуля `k8s.io/metrics`); UI
  запрашивает `GetMetrics` для видимой таблицы раз в 15 с, backend кэширует ответ на 10 с.
- **Статус informer-а** — через обёртку `ListerWatcher`: успешный List/Watch → `ready`,
  ошибка → `stale` (были данные) / `error` с классом; запрет — класс `forbidden`.
- **Secrets в YAML** — значения `data`/`stringData` заменены на `<N bytes>`; раскрытие —
  бэклог (осознанное действие, отдельный запрос).
- **Problems, рост рестартов, палитра** — P5; планировщик дедлайнов нужен уже здесь для
  «Pending дольше N» в health pods.

## Файлы

```
internal/core/          ref.go, health.go, cell.go, kind.go, resource.go, scope.go
internal/provider/      session.go (Session, Delta, Query, ViewStatus, ошибки-классы)
internal/views/         view.go (hot-layer вида, журнал, надгробия), manager.go (Open/Get/Close,
                        события), *_test.go (фейковая Session)
internal/providers/kubernetes/
  session.go            Open: rest.Config из kubeContext (Files+Name), хеш конфига
  cache.go              informer-кэши: ключ, аренды, grace 60 с, LRU неактивных
  listwatch.go          обёртка ListerWatcher со статусом
  kinds.go              реестр kind: GVR, дескриптор, transform-whitelist, проекция, health
  kind_*.go             pods, workloads (deploy/sts/ds/rs), network (svc/ing), config (cm/secret),
                        nodes, events, namespaces
  health.go, deadline.go  правила health, планировщик дедлайнов
  resource.go           Get: полный объект, YAML без managedFields, маскирование Secret, связи
  metrics.go            metrics.k8s.io
internal/api/           новые методы + сессии target-ов
web/src/                views/useView.ts (курсорный протокол), components/{KindNav, ScopePicker,
                        ResourceTable, ResourceDrawer, YamlView (lazy CodeMirror), StatusCell}
tests/e2e/              kind-сценарии (отдельный конфиг, требует кластер)
scripts/kind-seed.sh    тестовые ресурсы: здоровые, crashloop, pending, imagepull, RBAC-юзер
```

## Задачи

### Task 1: core-типы и контракт Session
- [ ] `core`: `Ref`, `Health{State, Reason, Message, Issues}`, `Cell` (типизированное значение:
      text/number/age/ratio/status + null), `Column`, `KindDescriptor{ID, Title, Group, Columns,
      Scoped}`, `ScopeSel{Mode, Name}`, `Scope`, `Relation{Type, Ref}`, `Resource`.
- [ ] `provider`: `Session`, `Query{Kind, Scope, FieldSelector}`, `Delta` (Snapshot/Upsert/Delete/
      Status/Closed), `ViewStatus{State, Class, Message}`, классы ошибок; `Provider.Open`.
- [ ] Тесты: JSON-формы типов (стабильны для UI), валидация `ScopeSel`.

### Task 2: движок видов `internal/views`
- [ ] `View`: строки (неизменяемые снимки), `version`, журнал «id → версия изменения»,
      надгробия, `minRetained`, статус; `Apply(delta)`; `Since(v)` → `{reset, upserts, deleted,
      status, version}` атомарно; ограничение журнала (по числу), UID-замена = delete+add.
- [ ] `Manager`: `Open(session, query)` → непрозрачный id (эпоха + счётчик), горутина чтения
      `Delta`, `Coalescer` 100 мс → `view_changed{viewId, version}` (Key = viewId), `Close`,
      закрытие всех видов сессии.
- [ ] Тесты: since=0 → reset; инвалидация во время чтения; курсор вне диапазона → reset;
      пустой reset; delete/recreate одного имени; статус без изменения строк двигает версию;
      старый viewId после Close → `gone`; гонки (`-race`) с конкурентными Apply/Since.

### Task 3: Kubernetes-сессия и кэши
- [ ] `session.go`: rest.Config через `clientcmd` (loading rules = `kubeContext.Files`, override
      context), QPS/Burst, таймауты; хеш разрешённой конфигурации; dynamic + discovery клиенты.
- [ ] `listwatch.go` + `cache.go`: ключ (GVR, namespace, field selector); аренды; grace 60 с;
      LRU ≤ 8 неактивных; resync 0; transform; статус из обёртки ListerWatcher.
- [ ] Тесты на fake dynamic client: аренда/освобождение/grace (фейковые часы), LRU-вытеснение,
      повторное открытие = новый informer, статус forbidden/stale/ready.

### Task 4: kinds, проекции, health
- [ ] 10 kinds + namespaces (scopes) + replicasets (внутренний, для связей): GVR, колонки,
      whitelist transform, проекция в `Row`, health по правилам спецификации.
- [ ] Планировщик дедлайнов (Pending > 5 мин → Warning; Ready=false на старте — Progressing
      с grace), фейковые часы.
- [ ] Табличные тесты на фикстурах (YAML объектов): crashloop, imagepull, OOMKilled (lastState),
      pending unschedulable, terminating, succeeded job-pod, deployment rollout/desired=0,
      observedGeneration, node Ready=Unknown, service LB без адреса, secret без значений в кэше.

### Task 5: API и жизненный цикл сессий
- [ ] Методы: `ListKinds(target)`, `ListScopes(target)` (+ статус forbidden), `OpenView(target,
      kind, scope, fieldSelector)`, `GetRows(viewId, since)`, `CloseView(viewId)`,
      `GetResource(target, ref)`, `GetMetrics(target, kind, scope)`. HTTP + Wails + client.ts.
- [ ] Сессии: создаются по первому запросу target-а; при `targets_changed` сравнивается хеш —
      изменился/исчез → сессия и её виды закрываются (виды отдают `gone`); переключение
      target-а освобождает сессию без видов.
- [ ] Тесты: сервис с фейковым провайдером; транспорт — маршруты и коды ошибок.

### Task 6: детали, YAML, связи, метрики
- [ ] `GetResource`: полный объект (проверка UID), YAML без `managedFields` и last-applied,
      маскирование Secret; факты; связи: owners вверх, pods вниз (ownerRefs через RS / selector
      для svc), ingress → services, pod → node.
- [ ] `metrics.go`: наличие API через discovery; pods/nodes usage; кэш 10 с; нет API → пусто
      без ошибки.
- [ ] Тесты на fake клиентах.

### Task 7: Web
- [ ] `useView` — курсорный протокол (один запрос на вид, повтор пока применённая < объявленной,
      ограниченные повторы при ошибке, игнор старых ответов, `resync` → reset).
- [ ] `KindNav` (группы), `ScopePicker` (все/один; ручной ввод, если список запрещён),
      `ResourceTable` (TanStack Virtual, сортировка, фильтр, клавиатура ↑↓/Enter), `StatusCell`,
      возраст тикает на клиенте, CPU/RAM колонки.
- [ ] `ResourceDrawer`: факты, YAML (ленивый CodeMirror 6), Events объекта (вид), связи с
      переходом; Esc закрывает.
- [ ] Состояние по target-у в `target_state` (последний kind и scope).
- [ ] vitest: `useView` (все сценарии протокола), таблица, drawer; бандл-бюджет.

### Task 8: e2e и замеры на kind
- [ ] `scripts/kind-seed.sh`: namespaces, deployment, statefulset, daemonset, service, ingress,
      configmap, secret, crashloop, imagepull, pending; RBAC-пользователь только на один
      namespace (kubeconfig с токеном ServiceAccount).
- [ ] Playwright (`tests/e2e/kind.spec.ts`, запускается при наличии кластера): таблицы и health,
      живое обновление (scale), детали/YAML/events/связи, 403 → понятная ошибка, ручной namespace.
- [ ] Замеры: 5k ConfigMaps + 3k pending pods — пик и удержание памяти, время первой таблицы,
      повторная навигация, churn; `scripts/pss.sh`. Итоги — в этот план и спецификацию.
- [ ] Desktop под Xvfb: скриншот таблицы и drawer.

## Review Focus

1. Кластер недоступен/медленный при выборе context — UI не зависает, вид в `loading` → `error
   unavailable`, переключение на другой context мгновенно.
2. Права только на один namespace — список namespaces запрещён, но выбранный namespace
   работает; «все namespaces» — понятная ошибка, не пустая таблица.
3. Kubeconfig с exec-плагином (у пользователя `yc`), который долго отвечает или требует
   логина — таймаут и понятная ошибка, без зависания UI.
4. Быстрое переключение kind/namespace туда-обратно — нет утечки informers (аренды, LRU),
   старые ответы не перетирают новый вид.
5. Удаление и пересоздание объекта с тем же именем (pod контроллера) — строка обновляется,
   drawer не показывает чужой объект (проверка UID).
