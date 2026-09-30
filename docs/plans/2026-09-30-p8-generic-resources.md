# P8 — Kubernetes: все ресурсы API (CRD и не описанные виды)

> Для агентов: задачи выполняются по порядку, шаги отмечаются `- [x]`; каждая задача —
> отдельные коммиты `area: summary`, `make check` зелёный перед коммитом; каждое исправление и
> каждая возможность начинаются с теста, который падает без них.

**Цель:** в открытом kube context-е пользователь видит **все** ресурсы API, которые можно
list+watch, а не только 11 описанных видов: Custom Resources (cert-manager, Argo, свои CRD
SPK…) и встроенные виды без своей проекции (Jobs, CronJobs, PVC, PV, StorageClasses,
ServiceAccounts, Roles, HPA…). Колонки — **те же, что у `kubectl get`** (server-side Table:
`additionalPrinterColumns` у CRD, встроенные принтеры у остальных), живое обновление,
детали (факты, YAML, связи, события), удаление с тем же планом и подтверждением, палитра
(`:certificates`, короткие имена). Описанные виды P1 не меняются.

**Спецификация:** `docs/specs/2026-09-29-spk-ocular-design.md` — «Provider API» (виды
квалифицированы группой: `"apps/deployments"`, чтобы CRD разных групп не сталкивались; GVR
внутри адаптера), «Ключевые технические решения» (watch через общий кэш, никаких фоновых
опросов, действия: план → подтверждение → запуск по UID). Бэклог: «CRD-виды (generic-колонки,
`additionalPrinterColumns`)».

## Глобальные ограничения

- Всё из P0–P7 (AGENTS.md «Правила»). В `internal/core`, `internal/api` и общем UI не
  появляется Kubernetes-знание (discovery, Table, CRD) — только новые общие поля/события.
- Никаких фоновых опросов: набор ресурсов обновляется по событию (watch CRD, если разрешён)
  или явно (`F5` в навигации / переоткрытие target-а), не по таймеру.
- Права пользователя не предполагаются: discovery разрешён всем аутентифицированным
  (`system:discovery`), чтение CRD-объектов и list ресурса — нет; отказ показывается, а не
  прячет вид молча.
- Тестовые записи — только kind `ocular-dev`; кластеры пользователя не трогаются.
- UX важнее экономии памяти; считается только Private_Dirty (критерий soak — спецификация).

## Что уже есть (обзор 2026-09-30, карта — отчёт исследования)

- Вид: `core.KindDescriptor` (`internal/core/kind.go:46-87`: ID, Title, Singular, Group,
  Columns, Scoped, Aliases, Actions, EventsKind, Sort, NotCovered…); внутри провайдера
  `kindDef{desc, gvr, namespaced, keep, pre, project, virtual}` (`kinds.go:13-30`). Реестр —
  **глобальный фиксированный** `allKinds` (`session.go:31-37`), алиасы/единственное число/
  действия — отдельные карты по `*kindDef` (`session.go:40,57`, `actions.go:45`), слияние в
  `descriptors()` (`kinds.go:130-144`).
- API: `ListKinds` (`api.go:27`) → `sess.Kinds()`; `OpenView` отвергает неизвестный вид
  (`sessions.go:189-196`). UI: `Workspace.tsx:112` — `listKinds` **один раз** на target,
  группы навигации — по `k.group` в порядке бэкенда.
- Кэш (`cache.go`): ключ `{gvr, namespace, selector}`, `dynamic.Interface` + unstructured
  informer, transform по `keep`/`pre` — **универсален по GVR**. Проекция —
  `func(u, now) ([]core.Cell, core.Health, time.Time)`, ячейки по позиции колонок.
- Детали: `Get` (dynamic, UID, YAML без managedFields/last-applied) и факты из ячеек —
  универсальны; связи вверх по ownerReferences — универсальны, но `kindFor`/`kindOf`
  (`resource.go:152-194`) — switch по встроенным: владелец-CR виден, но не открывается.
  События объекта — `EventsKind="events"` по `involvedObject.uid` для всех.
- Delete (`actions.go:240-243`) — по `def.gvr` с предусловиями UID/resourceVersion,
  последствия (`actions_effects.go`) — частные случаи StatefulSet/ReplicaSet/Pod, иначе
  `delete.requested`; права — SSAR по `def.gvr`. Restart/scale — только workload-ы.
- Discovery и чтение CRD в коде **нет**. Problems: `NotCovered` называет «custom resources».
- Палитра: алиасы от провайдера (`kindAliases`), UI сопоставляет id, последний сегмент id,
  title и алиасы (`web/src/palette/items.ts`).

## Решения P8

### 1. Набор ресурсов — discovery, не чтение CRD

- На сессию — **реестр видов сессии**: статические виды P1 + обнаруженные. Обнаружение —
  discovery client-go (`ServerPreferredResources`: предпочтительная версия группы, как у
  kubectl; aggregated discovery, если сервер умеет — один запрос). Берутся ресурсы с
  глаголами `list` **и** `watch`, не subresource-ы, не покрытые статическими видами (по
  GVR-группе+ресурсу, любой версии), не `events` (оба API событий — статический вид).
- Ошибка discovery части групп (`ErrGroupDiscoveryFailed`, например упавший aggregated API)
  — частичный набор + заметка в навигации «Не удалось получить: <группы>»; полный отказ —
  только статические виды и заметка.
- **Обновление набора**: если разрешён list+watch `customresourcedefinitions` — informer
  CRD (общий кэш, keep — только имена/группа/версии) как **триггер** повторного discovery
  (с дебаунсом 1 с); иначе — повтор по `F5` в навигации и при переоткрытии target-а. Набор
  меняется атомарно; UI узнаёт о смене событием (решение 5).
- Kind ID обнаруженного: `<group>/<plural>` (core-группа — `<plural>`), Title — `Kind` во
  множественном по discovery (`plural` с заглавной, как Lens), Singular — `singularName`
  (или `Kind`), Aliases — `shortNames` + `singularName` + `plural`, Scoped — `namespaced`.
  Группа навигации — решение 4.

### 2. Колонки и строки — server-side Table (как `kubectl get`)

- Список и watch обнаруженного вида — с `Accept: application/json;as=Table;v=v1;g=meta.k8s.io`
  и `includeObject=Object`: сервер сам считает колонки (`additionalPrinterColumns` CRD,
  встроенные принтеры) и ячейки; объект строки приходит целиком и **обрезается** transform-ом
  кэша до `metadata` (без managedFields и annotations) и `status.conditions` (для health) —
  остальное не нужно: ячейки уже посчитаны сервером.
- Адаптер для informer-а: `ListFunc` превращает Table в список «строк-объектов»
  (unstructured: metadata строки + `cells` + `columns`-версия), `resourceVersion` списка — из
  Table; `WatchFunc` — watch в формате Table, каждое событие (одна строка) → строка-объект;
  `BOOKMARK`, `ERROR`/410 → как у обычного watch (relist). Спайк Task 0 подтверждает формат
  на kind; сервер без watch в формате Table (старые версии) → список обычным форматом с
  колонками Name/Namespace/Age (спайк проверяет поведение отказа).
- Колонки вида: `columnDefinitions` с `priority == 0` (как `kubectl get` без `-o wide`);
  типы `integer`/`number` → `number`, `date` → `age` (из значения ячейки), остальные —
  `text`; первая колонка `Name` (format `name`) → имя строки; встроенная «Age» (строка
  «5m» у встроенных принтеров) заменяется своей колонкой `age` из
  `metadata.creationTimestamp` (тикает сама); колонка namespace — общая (`ScopeColumn`).
  Колонки определяются **первым ответом** list-а и закрепляются за видом до смены набора
  (решение 1); `KindDescriptor` вида до первого ответа — Name/Namespace/Age.
- Wide-колонки (`priority > 0`) — в фактах деталей (свежий `Get` в формате Table для
  одного объекта), не в таблице.
- Health строки (универсально, из обрезанного объекта): `deletionTimestamp` → terminating;
  condition `Ready` (или `Available`, если `Ready` нет): True → ok, False → error с
  reason/message, Unknown → progressing; иначе condition `Failed`/`Degraded` True → error,
  `Stalled` True → warning; иначе — нейтрально (`unknown` без акцента, как сейчас у пустых
  health — проверить в UI). Правило — в спецификации, не эвристика по тексту ячеек.

### 3. Детали, связи, действия

- `Get` — как сейчас (dynamic, YAML); факты — ячейки строки (включая wide из Table `Get`).
- `kindFor`/`kindOf` — по реестру сессии (apiVersion+Kind → вид), владелец-CR открывается.
- Delete — у каждого обнаруженного вида с глаголом `delete`: общий план, последствия
  `delete.requested` + **финализаторы** объекта («удаление ждёт финализаторы: …» — у CR это
  частый случай «висит в Terminating»); права — SSAR по GVR. Scale по `subresources.scale` —
  бэклог.
- Problems не расширяется (CR остаются в `NotCovered`).

### 4. Навигация

- Статические разделы не меняются. Новые разделы: «Прочее API» (обнаруженные встроенные
  группы: core, batch, autoscaling, rbac…) и «Custom Resources» (группы CRD — не
  `*.k8s.io` и не встроенные). Внутри — подгруппы по API-группе, **свёрнуты по умолчанию**,
  раскрытие сохраняется в `target_state`; подгруппа с одним видом — без уровня. Счётчик
  видов у заголовка. Палитра находит любой вид по имени/короткому имени без раскрытия.
- Метаданные для UI — общие поля `KindDescriptor.Subgroup` (подпись подгруппы) и порядок от
  провайдера; UI не знает, что это API-группы.

### 5. Смена набора видов в открытом UI

- Событие хаба «виды target-а изменились» (номер версии набора) → UI перечитывает
  `ListKinds`; открытый вид, пропавший из набора, получает статус `gone` («Ресурс больше не
  обслуживается API») и не переоткрывается сам; вид с изменившимися колонками —
  переоткрывается (новый `viewId`).

## Задачи

### Task 0. Спайк на kind
- [ ] Table list/watch через dynamic/REST клиент с `as=Table` и `includeObject=Object`: формат
  событий (одна строка на событие?), BOOKMARK, 410, `resourceVersion`; колонки CRD с
  `additionalPrinterColumns` (типы, `date` — строка RFC3339?) и встроенных (Jobs, PVC — «Age»
  строкой); aggregated discovery client-go на kind. Итог — в «Спайк» этого плана.

### Task 1. Реестр видов сессии и discovery (решение 1)
- [ ] Реестр сессии вместо `allKinds` (алиасы/единственное число/действия — внутрь `kindDef`);
  discovery-фильтр; ID/алиасы; частичные ошибки. Тесты на фейковом discovery: CRD и
  встроенный вид появляются, статические не дублируются, subresource/без watch — нет,
  частичный отказ группы — заметка.
- [ ] Триггер CRD-informer-ом (разрешён) / явный повтор (запрещён); событие смены набора;
  `OpenView` пропавшего вида → `gone`.

### Task 2. Table-кэш и проекция (решение 2)
- [ ] Адаптер list/watch Table → строки-объекты, transform, колонки по первому ответу, Age,
  health по conditions. Тесты на тестовом сервере (httptest): list, watch-события, 410 →
  relist, смена колонок.

### Task 3. Детали, связи, delete (решение 3)
- [ ] Факты с wide-колонками, `kindFor` по реестру, delete с финализаторами. Тесты.

### Task 4. UI: навигация, события набора, палитра (решения 4–5)
- [ ] Разделы/подгруппы со сворачиванием и `target_state`, перечитывание по событию, `gone`
  у пропавшего вида; vitest + synth e2e.

### Task 5. kind и e2e
- [ ] `kind-seed`: CRD `widgets.ocular.dev` (namespaced, printer columns: число, строка,
  дата, condition Ready; shortName `wd`) и cluster-scoped `gadgets.ocular.dev`, CR-ы с Ready
  True/False, финализатор; Job, CronJob, PVC. `TestKind…`: колонки как у `kubectl get`,
  живое изменение, CRD добавлен при открытом target-е → вид появился, удалён → `gone`,
  viewer без list ресурса → forbidden в виде, без чтения CRD → набор всё равно есть.
  e2e-kind: навигация, таблица CR, детали, delete CR с финализатором.

### Task 6. Desktop, документы, ревью
- [ ] Desktop под Xvfb (русская раскладка): навигация с подгруппами, таблица CR, детали,
  палитра `:wd` — скриншоты; Private_Dirty с открытыми CR-видами.
- [ ] AGENTS.md, спецификация (P8 ✅), бэклог, «Итоги»; гейты `make check`, kind.
- [ ] Ревью реализации Codex, исправления, раздел «Ревью реализации».

## Фокус ревью (что тесты задач могут не поймать)

1. Набор видов меняется, пока вид открыт или его запрос в полёте: нет гонки между старым и
   новым реестром (вид не открывается по устаревшему GVR, `gone` — а не ошибка).
2. Watch в формате Table после 410/обрыва: relist не теряет и не дублирует строки, колонки
   не «съезжают» при смене версии CRD.
3. Кластер с сотнями CRD: discovery не блокирует открытие target-а (статические виды
   доступны сразу), навигация не тормозит, память — в запасе.
4. Права: discovery есть, list ресурса запрещён — вид показывает отказ; list CRD запрещён —
   набор всё равно собирается, обновление — по `F5`.
