# P11 — узлы: cordon, uncordon, drain

> Для агентов: задачи выполняются по порядку, шаги отмечаются `- [x]`. Каждая задача — отдельные
> коммиты `area: summary`, `make check` зелёный перед коммитом. Каждое исправление и каждая
> возможность начинаются с теста, который падает без них.

**Цель:** у узла Kubernetes появляются действия **Cordon** (запретить новые pod-ы),
**Uncordon** (разрешить снова) и **Drain** (cordon + выселить pod-ы через Eviction API с
учётом PodDisruptionBudget) — как `kubectl cordon/uncordon/drain` и те же пункты Lens. Сейчас у
узлов нет ни одного действия (P4 сознательно не дал им delete: «узел выводят, а не удаляют»).

**Архитектура:** существующий контракт действий P4 (`PrepareAction` → просмотр → один
`RunAction`), без нового API. Drain — первое действие Kubernetes с частями (`ActionResult.Parts`,
как у Compose P7): cordon и выселение каждого pod-а — отдельные записи, итог по частям. Общий UI
и `internal/core` не знают слов «узел» и «drain» (подписи — ключи сообщений провайдера).

**Спецификация:** `docs/specs/2026-09-29-spk-ocular-design.md`; пункт — из `docs/backlog.md`
(«Действия (из P4): … cordon/drain узла»). Принципы: Read-first, destructive — с подтверждением,
все contexts RW без read-only-пометок.

## Глобальные ограничения

- Всё из P0–P10 (AGENTS.md «Правила»), особенно правила действий: `RunAction` читает строго по
  UID, `Expect` связывает всё, что прочитали эффекты плана, несовпадение — `conflict` без
  записи; каждая запись — один HTTP-запрос (`restWriter`, `MaxRetries(0)`), неоднозначный ответ
  (5xx, таймаут) — `unknown`, без повторов; повтор — только при провале предусловия, когда
  перечтение доказывает, что записи не было (≤ 3).
- Никаких опросов: drain **не ждёт** завершения выселенных pod-ов (это делает kubelet по их grace
  period); таблица pod-ов показывает это вживую. Итог говорит «выселение запрошено».
- Все contexts RW. Тесты записи — только kind `ocular-dev`, и только на отдельном узле для
  drain-тестов (Task 1), чтобы не выселять системные pod-ы и фикстуры других тестов.

## Решения P11 (агент, 2026-09-30)

1. **Объём.** Узел: `cordon`, `uncordon`, `drain`. Без параметров (новый вид параметра не
   нужен): grace period — собственный у каждого pod-а (как `kubectl drain` по умолчанию), без
   `--force`, без `--disable-eviction`, без ожидания. В бэклог: выбор grace period/таймаута,
   drain нескольких узлов разом, ожидание ухода pod-ов с прогрессом, запуск/приостановка
   CronJob (обнаруженный вид — отдельный этап).
2. **Cordon / Uncordon.** Merge patch `spec.unschedulable: true|false` с `metadata.uid` и
   `metadata.resourceVersion` (как restart/scale P4). Недоступно (`Unavailable`), если узел уже
   в нужном состоянии («уже закрыт для новых pod-ов» / «уже открыт»). Cordon не опасный;
   uncordon не опасный. Эффект: «новые pod-ы не будут назначаться на узел; работающие
   остаются». Права — SSAR `patch nodes`. `effectState` — `spec.unschedulable`.
3. **Drain: что выселяется.** Pod-ы узла — list по `spec.nodeName=<узел>` во всех namespace-ах
   (field selector, один запрос; нет права list pods во всех namespace-ах или ответ обрезан —
   план `Unavailable`: «не удалось узнать pod-ы узла», без записи; drain вслепую не делаем).
   Разбор как у `kubectl drain`:
   - **пропускаются** (в плане, но без записи): pod-ы DaemonSet-ов (контроллер сразу вернёт их
     на узел), mirror/static pod-ы (аннотация `kubernetes.io/config.mirror` — API их не
     выселит), уже завершённые (`Succeeded`/`Failed`) и уже удаляемые (`deletionTimestamp`);
   - **не выселяются, названы** — pod-ы без контроллера (нет `ownerReferences` с
     `controller: true`): после выселения их никто не пересоздаст; `kubectl` без `--force`
     отказывается. Мы не отказываемся от всего drain: такие pod-ы остаются на узле, план и
     итог называют их («удалите сами, если они не нужны»);
   - **выселяются** — остальные. Pod-ы с `emptyDir` выселяются, но план называет их: данные
     `emptyDir` пропадут (эффект опасный — как `--delete-emptydir-data`).
   Drain всегда опасный (pod-ы перезапускаются в другом месте, возможен простой).
4. **Drain: PDB.** Выселение через `POST pods/<pod>/eviction` (`policy/v1` Eviction) с
   `deleteOptions.preconditions.uid` pod-а. Ответ 429 (бюджет PDB не позволяет сейчас) — часть
   `refused` со словами «не позволяет PodDisruptionBudget, повторите позже»; без повторов и
   ожидания (kubectl повторяет каждые 5 с — это опрос, мы не делаем; повторный drain того же
   узла безопасен: cordon уже стоит, выселены будут оставшиеся). 404/409 по uid — pod уже ушёл
   или заменён: `skipped`. План заранее предупреждает, если на pod-ы узла распространяются PDB
   с `status.disruptionsAllowed == 0` (list PDB по namespace-ам этих pod-ов, по selector-у;
   не удалось прочитать — «PDB не проверены», не «PDB нет»).
5. **Drain: порядок и части.** Части: `cordon` (если узел ещё не закрыт), затем по одной части
   на каждый выселяемый pod (namespace/имя), затем части `skipped` для названных «без
   контроллера». Cordon первым: без него контроллеры могут вернуть pod-ы на этот узел. Отказ
   или `unknown` cordon-а — остальные части `skipped` (выселять без cordon нельзя).
   Отказ одного выселения **не** останавливает остальные: выселения независимы (в отличие от
   Compose, где члены — одно целое), а остановка по первому PDB-отказу оставила бы узел
   наполовину выведенным без причины. `unknown` одного выселения (таймаут) — тоже не
   останавливает. Общий срок прогона — `drainRunTimeout` (60 с); не успевшие части — `skipped`
   («не начато: истёк срок»). Итог — `partsSummary` (как P7).
6. **Drain: Expect.** Связывает узел (uid, `spec.unschedulable`) и **набор выселяемых pod-ов**
   (uid каждого, отсортированно) и названных без контроллера, а также наличие `emptyDir` у
   выселяемых. В `RunAction` узел перечитывается по uid, pod-ы узла — тем же list; другой
   набор — `conflict` без записи («pod-ы узла изменились — посмотрите снова»). Изменения
   пропускаемых (DaemonSet, mirror, завершённые) в `Expect` не входят: они не пишутся.
   PDB в `Expect` не входят: их состояние меняется само и проверяется сервером при каждом
   выселении (предупреждение плана — только прогноз, так и сказано).
7. **Права.** Cordon/uncordon — SSAR `patch nodes`. Drain — `patch nodes` и `create
   pods/eviction` в каждом namespace выселяемых pod-ов (SSAR по namespace, параллельно, под
   общим `prepareExtrasTimeout`); отказ в любом — план `denied` с перечнем namespace-ов;
   не проверено — «unknown», не «allowed».
8. **Где видно.** Меню строки узла и «Действия ▾» в деталях: Cordon, Uncordon, Drain (подписи
   `act.cordon/uncordon/drain`, RU «Закрыть для pod-ов», «Открыть для pod-ов», «Вывести
   (drain)»). Из недоступных показывается только применимое по строке (закрытый узел — без
   Cordon, открытый — без Uncordon); план всё равно проверяет по свежему объекту. Просмотр
   drain: число выселяемых, список (namespace/имя, владелец) порциями как разница P9, отдельно
   — без контроллера, с `emptyDir`, пропускаемые (свёрнуто), PDB-прогноз, права.
9. **Тестовый узел.** kind `ocular-dev` пересоздаётся с конфигом: control-plane + один
   worker с taint-ом `ocular.dev/drain=only:NoSchedule` и меткой `ocular.dev/drain=only`.
   Туда попадают только pod-ы drain-фикстур (toleration + nodeSelector), системные и прочие
   фикстуры остаются на control-plane. Существующие тесты, считающие узлы (`kind_test.go`
   «один узел»), и `kind-churn.sh` правятся на два узла.

## Контракт

Нового API нет. Изменения:
- `internal/providers/kubernetes/actions.go`: `actCordon`, `actUncordon`, `actDrain`; у
  `nodesKind` — все три.
- Kubernetes `RunAction` для drain возвращает `ActionResult{Outcome, Parts}` (как Compose);
  `failedWrite`/`restWriter` — для eviction (субресурс `eviction`, POST).
- Сообщения `kubernetes.node.*` (EN+RU) и `act.cordon/uncordon/drain` в `web/src/i18n.ts`.

## Задачи

### Task 0. План
- [ ] План, ревью плана Codex, правки плана до закрытия.

### Task 1. kind: второй узел
- [ ] `scripts/kind-config.yaml` (control-plane + worker с taint/меткой); `make kind-up`
  создаёт кластер с `--config` по нему; пересоздание `ocular-dev` (`kind-down`, `kind-up`, seed,
  rbac, metrics).
- [ ] Правка тестов, считающих узлы, и `kind-churn.sh`; `make test-kind` и `make e2e-kind`
  зелёные на двух узлах до любых изменений кода действий.

### Task 2. Cordon / Uncordon (Go)
- [ ] Unit: план (эффект, `Unavailable` в нужном состоянии, права SSAR, `effectState`), запись
  (merge patch с uid+rv; 409 с тем же `Expect` — повтор; другое состояние — `conflict`).
- [ ] kind: cordon → `spec.unschedulable` true, uncordon → false; повтор — `Unavailable`.

### Task 3. Drain (Go)
- [ ] Разбор pod-ов узла (unit, таблица случаев: DaemonSet, mirror, завершённый, удаляемый,
  без контроллера, `emptyDir`, обычный), `Expect` по набору, PDB-прогноз, права по
  namespace-ам, list запрещён/обрезан — `Unavailable`.
- [ ] Прогон: cordon первым; отказ cordon — остальные `skipped`; 429 — `refused` (PDB), 404/409
  — `skipped`, 5xx/таймаут — `unknown`, остальные продолжаются; срок прогона; изменённый набор
  — `conflict` без записи; каждый запрос — один (счётчик запросов в httptest).
- [ ] kind: на тестовом узле Deployment (2 реплики), pod без контроллера, pod с `emptyDir`,
  PDB `minAvailable: 2` на отдельном Deployment; drain: cordon, выселения, PDB-отказ, без
  контроллера остаётся; uncordon в конце (очистка).

### Task 4. UI
- [ ] Подписи, меню узла (применимое по строке), просмотр drain (списки порциями, свёрнутые
  пропускаемые), итог по частям (уже есть у P7) — vitest.
- [ ] e2e-kind: cordon/uncordon узла, drain тестового узла с PDB-отказом в итоге.

### Task 5. Desktop, документы, ревью
- [ ] Desktop под Xvfb в русской раскладке (скриншоты просмотра drain и итога), Private_Dirty.
- [ ] AGENTS.md (правила drain), спецификация P11 ✅, backlog, «Итоги» плана.
- [ ] Ревью реализации Codex до закрытия.
