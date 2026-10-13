# SPK Ocular 1.6.0

## Comandos de agentes em contêineres

- Agentes locais descobrem instâncias e preparam/executam comandos finitos via `GetExecInfo`, `PrepareExec` e `RunExec`. stdout/stderr separados são retornados em base64, com estado de saída observado e limites de saída e duração.
- A permissão separada `exec` exige tipos nomeados e escolha explícita da instância/contêiner. Cada comando requer confirmação por padrão com argv, stdin finito, destino e limites exatos. Não há shell interativo persistente, shell implícito, attach ou repetição automática.
- Permanecem os limites de namespaces Kubernetes e projetos Docker Compose, identidade, UID e revisão de configuração. Alterações de permissões cancelam o transporte; encerrar o processo remoto é best effort. Resultados incompletos ou desconhecidos são explícitos. Contêineres Docker independentes não herdam permissões de projetos.
- Exec usa as permissões do contêiner e pode ler segredos montados ou alterar dados do contêiner/host. Comandos, entrada e saída não são mantidos no registro de auditoria. Permissões existentes não recebem exec automaticamente.

## Correções

- A confirmação de exclusão de downloads foca Cancelar após a preparação.
- A entrada finita Docker envia EOF preservando stdout/stderr; comandos sem entrada desconectam stdin. Transportes incompatíveis são recusados antes do início.
- As confirmações alemãs exibem texto corretamente codificado. Os controles e confirmações exec estão traduzidos nos oito idiomas.

Os pacotes mantêm o executável unificado desktop/navegador e a verificação nas seis combinações nativas de sistema e arquitetura.
