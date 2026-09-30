# P12 — CronJob: приостановить, возобновить, запустить сейчас

> Для агентов: задачи выполняются по порядку, шаги отмечаются `- [x]`. Каждая задача — отдельные
> коммиты `area: summary`, `make check` зелёный перед коммитом. Каждое исправление и каждая
> возможность начинаются с теста, который падает без них.

**Цель:** у CronJob появляются действия **Suspend** (новые запуски по расписанию не
начинаются), **Resume** (начинаются снова) и **Run now** (создать Job из шаблона CronJob
сейчас — как `kubectl create job --from=cronjob/<имя>` и «Trigger» в Lens). Сейчас CronJob —
обнаруженный вид (P8: колонки сервера, в разделе Workloads) с единственным действием Delete.

**Архитектура:** контракт действий P4 (`PrepareAction` → просмотр → один `RunAction`) без
новых методов API и без изменений `internal/core`/общего UI. Обнаруженный вид получает
действия по точному `GroupResource` (`batch/cronjobs`) рядом с `delete`; колонки остаются
серверными (Table: Schedule, Suspend, Active, Last Schedule — меняются вживую). Все подписи —
ключи сообщений провайдера.

**Спецификация:** `docs/specs/2026-09-29-spk-ocular-design.md`; пункт — из `docs/backlog.md`
(«CronJob: действия (suspend/resume, запустить Job) — отдельный этап», решение 148 P11).
Принципы: Read-first, destructive — с подтверждением, все contexts RW без read-only-пометок.

## Глобальные ограничения

- Всё из P0–P11 (AGENTS.md «Правила»), особенно правила действий: `RunAction` читает строго по
  UID, `Expect` связывает всё, что прочитали эффекты плана, несовпадение — `conflict` без
  записи; каждая запись — один HTTP-запрос (`restWriter`, `MaxRetries(0)`), неоднозначный ответ
  (5xx, таймаут, обрыв) — `unknown`, без повторов; повтор — только при провале предусловия,
  когда перечтение доказывает, что записи не было (≤ 3). Маршрут (GVR + scope) — в `Expect` и
  на весь прогон (P8).
- Никаких опросов: «Run now» не ждёт завершения Job — таблица Jobs/Pods показывает это вживую.
- Все contexts RW. Тесты записи — только kind `ocular-dev`, в собственном namespace теста.

## Решения P12 (агент, 2026-09-30)

1. **Объём.** `batch/v1` CronJob: `suspend`, `resume`, `run` (Run now) + существующий `delete`.
   Действия привязаны к точному GR `batch/cronjobs` и только когда discovery даёт версию `v1`
   (иной версии нет с Kubernetes 1.25; другая — действий нет, только delete) и глаголы
   `patch` (suspend/resume), `get` (все). Не делаем: suspend/resume Job, выбор имени Job,
   изменение шаблона перед запуском, переход к созданной Job (бэклог).
2. **Suspend / Resume.** Merge patch `spec.suspend: true|false` с `metadata.uid` и
   `metadata.resourceVersion` (как cordon P11, повтор по 409 — общее правило). Недоступно,
   если CronJob уже в нужном состоянии (`suspend` отсутствует = `false`). Оба не опасные.
   Эффекты:
   - suspend: «новые запуски по расписанию не начнутся; уже идущие Job продолжаются» (+ число
     активных из `status.active`, если есть);
   - resume: «запуски по расписанию возобновятся»; «время запуска, пропущенное за
     приостановку, может начать один запуск сразу: без `startingDeadlineSeconds` — если
     пропусков не слишком много; с ним — если пропуск был не раньше N с назад» (формулировка
     «может»: решает контроллер; поведение по документации Kubernetes, CronJob «Schedule
     suspension»).
   `effectState` — `spec.suspend` (для resume ещё `startingDeadlineSeconds`, по нему текст).
   Права — SSAR `patch cronjobs` (группа `batch`) с namespace и именем.
3. **Run now: что создаётся.** Ровно то, что делает `kubectl create job --from=cronjob`
   (kubectl `createJobFromCronJob`): `batch/v1` Job в namespace CronJob-а; `metadata.labels` —
   из `spec.jobTemplate.metadata.labels`; `metadata.annotations` — из шаблона плюс
   `cronjob.kubernetes.io/instantiate: manual`; `ownerReferences` — один:
   `{apiVersion: batch/v1, kind: CronJob, name, uid, controller: true}` (без
   `blockOwnerDeletion`, как kubectl); `spec` — `spec.jobTemplate.spec` как есть. Запуск не
   зависит от расписания и от `suspend` (приостановленный CronJob тоже можно запустить).
4. **Run now: имя и `Expect`.** Имя — `<cronjob>-manual-<5 символов [a-z0-9]>`; имя Job
   входит в значения меток её pod-ов (`job-name`, ≤ 63), поэтому часть имени CronJob-а (сама
   ≤ 52) усечена до 50, всё имя ≤ 63; усечение не оставляет `-` в конце. Имя
   выбирается **в плане** и показывается в просмотре («будет создана Job <имя>»). `Expect` run =
   `<маршрут>-<хэш состояния>.<суффикс>`: хэш — UID CronJob-а и SHA-256 канонического
   `spec.jobTemplate` (метаданные и spec), `deletionTimestamp`; суффикс — те самые 5 символов.
   `RunAction` проверяет маршрут, пересчитывает хэш по свежему чтению по UID (шаблон изменился
   — `conflict`, ни одной записи), проверяет суффикс (`^[a-z0-9]{5}$`, иначе `invalid`) и
   создаёт Job с этим именем. Так показанное имя = созданное, а один просмотр не создаёт двух Job.
5. **Run now: запись и ответы.** Один `POST jobs` (`restWriter`, `MaxRetries(0)`), без
   предусловий на CronJob (у create их нет): окно между чтением шаблона и созданием —
   одно чтение по UID перед записью, это сказано в правилах. Ответы:
   - 201 — «Job <имя> создана» (результат несёт имя в сообщении);
   - 409 AlreadyExists — `refused`: «Job с таким именем уже есть — посмотрите снова» (новый
     просмотр даст новое имя); повторов нет;
   - 403 — `forbidden`; 400/422 — `refused` с текстом сервера (квоты, admission);
   - 5xx/таймаут/обрыв/нет статуса — `unknown`: «проверьте Jobs, есть ли <имя>, прежде чем
     повторять»; повторов нет никогда (create не идемпотентен).
   Своя ветка классификации (`failedWrite` — про объект действия: 404/409 там значат другое).
6. **Run now: просмотр.** Не опасный (создаёт, ничего не удаляет). Эффекты: «будет создана
   Job <имя> из шаблона CronJob-а сейчас, вне расписания»; «Job принадлежит CronJob-у
   (ownerReference controller) — его пределы истории (`successfulJobsHistoryLimit`/
   `failedJobsHistoryLimit`) касаются и её; удаление CronJob удалит её»; «`concurrencyPolicy`
   к ней не применяется: запуск по расписанию её не заменит и не будет ею остановлен» —
   каждое утверждение проверяется на kind (Task 4) и формулируется по результату. Предупреждения:
   приостановлен («CronJob приостановлен: запуск всё равно будет»); сейчас активны N запусков
   (`status.active`) — «эта Job пойдёт параллельно». Права — SSAR `create jobs` (группа
   `batch`) в namespace CronJob-а.
7. **Где живёт код.** `internal/providers/kubernetes/cronjob.go`: дескрипторы
   `actSuspend`/`actResume`/`actRunNow`, привязка к обнаруженному виду (из `catalog.go`, по
   GR и версии), эффекты, недоступность, `Expect` с суффиксом, построение Job, классификация
   ответа create. `actionWriter.create` (restWriter: `POST` коллекции, `MaxRetries(0)`;
   dynWriter: `Create`). `RunAction` отправляет `run` в свою ветку (как drain), suspend/resume —
   общий путь `write`.
8. **Тестовые фикстуры.** kind: CronJob в namespace теста (не `ocular-crd` — там фикстура P8
   `yearly`, на которую смотрят другие тесты), расписание раз в год, `suspend: true`, шаблон —
   `busybox:1.36` `true` (образ уже на узлах, сидится P8); Job заканчивается за секунды.
   Очистка — удаление namespace.

## Задачи

### Task 1. Привязка действий к CronJob (Go)
- [ ] Обнаруженный вид `batch/v1` `cronjobs` получает `suspend`, `resume`, `run` (+ `delete`
  по глаголу); другая версия/без `patch` — без suspend/resume; другие виды не меняются
  (тест каталога). Подписи `act.*` и ключи сообщений EN + RU (`TestTheUIsTranslationsCoverEveryMessage`).

### Task 2. Suspend / Resume (Go)
- [ ] Просмотр: эффекты (активные, `startingDeadlineSeconds`), недоступность в том же
  состоянии, права по имени; `Expect` связывает `suspend`, `startingDeadlineSeconds`.
- [ ] Прогон: merge patch с uid + rv (тело запроса проверено), 409 со сменой версии — повтор,
  изменённый `suspend` — `conflict`. kind: suspend → resume фикстуры, поле на сервере.

### Task 3. Run now (Go)
- [ ] План: имя (усечение длинного имени CronJob-а до 50, без `-` в конце, алфавит суффикса), эффекты и
  предупреждения (приостановлен, активные), права `create jobs`; `Expect` с суффиксом.
- [ ] Прогон: Job как у kubectl (метки, аннотации + `instantiate: manual`, ownerReference
  controller, spec шаблона) — сравнение тела запроса; шаблон изменён после просмотра —
  `conflict`, ноль записей; неверный/чужой суффикс — `invalid`; 201, 409 AlreadyExists, 403,
  422, 503 с `Retry-After`, обрыв — ровно один POST на провод (`actions_wire_test.go`), без
  повторов.
- [ ] kind: запуск приостановленной фикстуры создаёт Job с показанным именем, владельцем и
  аннотацией; Job завершается.

### Task 4. Поведение контроллера на kind (проверка слов плана)
- [ ] Проверить и записать: ручная Job с `controller: true` попадает под пределы истории
  CronJob-а (history limit 1 → старая завершённая удаляется) или нет; учитывается ли в
  `status.active`; `concurrencyPolicy: Forbid` не мешает ручному запуску. Тексты эффектов
  решения 6 — по результату (что не подтвердилось — убрать или сказать «может»).

### Task 5. UI и e2e
- [ ] vitest: меню CronJob (Suspend/Resume/Run now/Delete), просмотр run с именем.
- [ ] e2e-kind: suspend → колонка Suspend `True` вживую, resume → `False`; Run now → в
  уведомлении имя, Job с этим именем появляется в Jobs.

### Task 6. Desktop, документы, ревью
- [ ] Desktop под Xvfb в русской раскладке (просмотр Run now, итог), Private_Dirty.
- [ ] AGENTS.md (правила CronJob-действий), спецификация P12 ✅, backlog, «Итоги» плана.
- [ ] Ревью реализации Codex до закрытия.
