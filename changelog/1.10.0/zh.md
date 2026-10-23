# SPK Ocular 1.10.0

- 通过进程内 MCP 服务器连接本地智能体，保留显式授权和操作审查。可按需配置 Codex 与 Claude Code，其他客户端可使用手动配置。 MCP Streamable HTTP 替代旧的本地智能体协议，现有客户端需要重新配置。
- 原生 Argo CD 和 Flux 视图展示应用、控制器状态与资源链接。编辑和扩缩容审查显示可用的 GitOps 归属信息。
- 通过显式启动、有范围限制的诊断读取，排查 workloads、Service/Ingress 网络路径和 Helm 发布，并显示来源与证据缺口。
- 添加标签列并保存资源表格视图。查看 cert-manager Certificate 状态与续期证据，无需读取 Secret 内容。
- 读取明确选择的 Prometheus 指标历史和已有 VPA 建议，并结合上下文检查 workload 配置与可靠性。
- 检查现有的命名空间 Trivy 配置/RBAC 报告及策略报告。集群级报告和指定 Gatekeeper 约束审计分别要求单独启用；缺少证据仍标为未知。
- 将 KRR 报告导入后端临时存储，或先审查临时 Kubernetes Job 再运行 KRR。查看选定 Node 按命名空间汇总的资源声明，不将其当作测量得出的空闲容量。
- 在本地导入有大小限制的离线 Polaris JSON 审计报告；导入过程不安装扫描器，也不查询集群。
- 合并明确选定 workloads 的日志，处理重叠，标明来源，并支持筛选和有限范围导出。
- 经审查后打开 Node Shell 和 Files 辅助工具，共享生命周期并持久记录清理结果。Files 支持删除及选中已显示文件；Alt+Shift+Enter 可分离 Files/Shell 窗口或重新停靠。
- 通过离线帮助和隔离的任务式入门课程学习 Kubernetes，提供更清晰的诊断反馈并支持八种界面语言。
- Pod 选择器解释 rollout 修订与就绪状态，比较提示更清楚。旧上游退出时，端口转发保留重连状态。
- 发布构建使用 Go 1.26.10，包含证书信任安全修复。
