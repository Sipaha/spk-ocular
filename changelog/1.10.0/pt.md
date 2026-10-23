# SPK Ocular 1.10.0

- Conecte agentes locais pelo servidor MCP integrado, com permissões explícitas e operações revisadas. Codex e Claude Code podem ser configurados a pedido; outros clientes têm configuração manual. MCP Streamable HTTP substitui o protocolo local anterior; os clientes existentes precisam de nova configuração.
- Consulte vistas nativas de Argo CD e Flux, estado de aplicações/controladores e ligações a recursos. As revisões de edição e escala mostram o contexto GitOps disponível.
- Investigue workloads, caminhos de rede Service/Ingress e releases Helm por leituras diagnósticas explícitas e limitadas, com fontes e lacunas visíveis.
- Adicione colunas de etiquetas e guarde vistas de tabelas. Consulte o estado e a renovação de Certificate do cert-manager sem ler o conteúdo de Secret.
- Leia o histórico Prometheus explicitamente escolhido e recomendações VPA existentes; reveja configuração e fiabilidade de workloads com o respetivo contexto.
- Inspecione relatórios existentes de configuração/RBAC do Trivy e de políticas por namespace. Relatórios de todo o cluster e a auditoria de uma restrição Gatekeeper escolhida exigem adesão separada; ausência de evidência continua desconhecida.
- Importe relatórios KRR para armazenamento temporário do backend ou reveja um Job Kubernetes temporário antes de executar KRR. Declarações de recursos por namespace de um Node selecionado não são capacidade livre medida.
- Importe localmente relatórios JSON Polaris limitados, sem instalar scanners nem consultar o cluster durante a importação.
- Combine logs de workloads explicitamente selecionados com tratamento de sobreposições, identificação de origem, filtros e exportação finita.
- Abra auxiliares Node Shell e Files após revisão, com ciclo de vida partilhado e registo duradouro da limpeza. Files permite eliminar e selecionar ficheiros revelados; Alt+Shift+Enter destaca ou volta a acoplar Files/Shell.
- Aprenda Kubernetes com ajuda offline e uma introdução isolada baseada em tarefas, diagnóstico mais claro e oito idiomas.
- A escolha de Pods explica a revisão do rollout e a prontidão; as orientações de comparação são mais claras. Encaminhamentos mantêm o estado de reconexão ao retirar upstreams antigos.
- Os builds usam Go 1.26.10, incluindo a correção de segurança da confiança em certificados.
