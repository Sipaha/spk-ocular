# SPK Ocular 1.7.0

- As listas do Kubernetes carregam por completo o tipo solicitado e os namespaces selecionados usando caches compactos compartilhados, pequenas páginas automáticas e watches. Um orçamento de leitura por sessão e a retenção limitada de caches inativos reduzem chamadas desnecessárias à API e memória retida.
- Consultas muito grandes de All Namespaces são interrompidas com um pedido para selecionar namespaces específicos, sem apresentar uma lista parcial como completa.
- Ícones de atualização uniformes releem as listas e giram até a conclusão. O carregamento mostra o tempo decorrido e o volume de dados de inventário recebido, quando disponível. Cancelar substitui a lista por um aviso persistente; Tentar novamente ou uma nova conexão reinicia o carregamento.
- O grafo do cluster só é carregado após um clique explícito em cada conexão. Selecionar, pesquisar e abrir detalhes preserva a câmera.
- Foram aprimorados a seleção de conexões, os rótulos de tipos, as dicas das tabelas, a navegação da paleta de comandos no escopo escolhido e a seleção de logs. Renderizar novamente as portas de um recurso não repete mais a mesma consulta de informações.
- As medições locais de inventário e memória, os dados brutos, a metodologia e os limites estão em `docs/performance.md`; não são garantias gerais de desempenho em produção.
- As listas evitam decodificações JSON repetidas, reduzindo o trabalho de CPU ao carregar grandes inventários Kubernetes.
