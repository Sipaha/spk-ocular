# Спайк: зависший exec-плагин аутентификации kubeconfig (2026-09-29)

**Вопрос** (из ревью Codex): прерывается ли запрос client-go, если exec-плагин (`yc`,
`aws`, `kubelogin`, …) завис — ждёт входа, нет сети?

**Опыт.** Kubeconfig с `exec: {command: hang.sh}` (скрипт делает `exec sleep 3600`),
client-go v0.37.1, dynamic `List` с `context.WithTimeout(2s)` и `rest.Config.Timeout = 2s`.

**Итог.** Запрос висел все 8 с наблюдения (и дальше) — ни контекст, ни таймаут не помогают:
`plugin/pkg/client/auth/exec/exec.go` `refreshCredsLocked` делает `exec.Command(...).Run()`
без контекста под мьютексом аутентификатора. После выхода тестового процесса плагин остался
сиротой (PPID сменился на реапер) — утечка процесса.

**Решение** (`internal/execshim`). `Wrap` переписывает `rest.Config.ExecProvider`: команда —
наш бинарь, `exec-credential-shim --timeout 90s -- <плагин> <аргументы>`. Shim запускает
плагин с тем же env (включая `KUBERNETES_EXEC_INFO`), stdin/stdout (протокол ExecCredential
проходит без изменений), в своей группе процессов; по таймауту убивает группу; сам умирает
вместе с приложением (`PR_SET_PDEATHSIG`), плагин — вместе с shim (`Pdeathsig`).
Проверено тестами: сквозной запрос client-go с зависшим плагином падает через таймаут
shim-а (`getting credentials: exec: … exit code 1`), процесс плагина убит; протокол
(stdin, env, аргументы, код выхода) проходит насквозь.

**Ограничения.** Сообщение client-go называет исполняемым файлом наш бинарь, а stderr плагина
уходит в лог приложения — UI формулирует ошибку сам по known auth-методу context-а.
Таймаут 90 с оставляет время на вход через браузер; интерактивные плагины (нужен терминал) в
GUI не работают и так (client-go: stdin не терминал → non-interactive).
