# SPK Ocular 1.6.0

## 代理容器命令

- 本地代理通过 `GetExecInfo`、`PrepareExec` 和 `RunExec` 发现容器实例并准备、执行有限命令。结果分别以 base64 返回 stdout/stderr，提供已观察到的退出状态以及输出和时间限制。
- 独立的 `exec` 权限要求明确指定资源类型和实例/容器。每条命令默认需要确认，显示准确的 argv、有限 stdin、目标和限制。不提供持久交互式 shell、隐式 shell、attach 或自动重放。
- 保留 Kubernetes namespace 和 Docker Compose project 边界、目标身份、UID 和配置版本检查。权限变更会取消活动传输；远程进程终止仅为 best effort。明确标示不完整或未知结果。独立 Docker 容器仍不继承项目权限。
- Exec 使用容器权限，可能读取挂载的机密或修改容器/主机数据。命令、输入和输出不保存在审计日志中。现有权限不会自动获得 exec。

## 修复

- 下载删除确认在准备完成后可靠地将焦点置于取消按钮。
- Docker 有限输入发送 EOF，同时保留 stdout/stderr；无输入命令断开 stdin。不支持的输入传输在启动前拒绝。
- 德语代理确认文字编码已修正。新 exec 控件和确认已翻译为全部八种界面语言。

软件包保留统一的桌面/浏览器可执行文件，并在全部六种原生操作系统/架构组合上进行验证。
