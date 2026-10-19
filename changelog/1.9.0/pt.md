# SPK Ocular 1.9.0

- Recursos, Helm, Graph, Timeline, Security e RBAC compartilham barras e controles consistentes de carga, progresso, cancelamento e nova tentativa sem apagar o conteúdo. Helm fica à esquerda após Storage.
- Propriedades de Pod separam contêineres normais, init e debug, mantêm rótulos Sidecar e quebram nomes e imagens longos.
- Datas usam hora local, formato de 24 horas e dd.mm.yyyy em todos os idiomas; o valor ISO original aparece ao passar o cursor.
- Escale Deployments diretamente nas propriedades. A revisão mostra o nome da conexão; botões menos/mais ajustam réplicas dentro dos limites antes da revisão.
- Escolher uma base de comparação não desloca mais a área de trabalho. Diferenças textuais incluem contexto e paginação limitada.
- Novas permissões container-exec abrangem o namespace/projeto escolhido sem seletor de tipos. Permissões restritas e confirmações existentes são preservadas; caixas ativas e desabilitadas ficam mais claras.
- Logs identificam conexão e recurso, usam todo o período e colocam o filtro antes da busca local. O backend percorre progressivamente um histórico recente limitado; resultados parciais aparecem durante a carga enquanto novas linhas são capturadas em paralelo. Limites e resultados incompletos são explícitos.
- Downloads abre automaticamente e salva na pasta de downloads do sistema sem perguntar. O clique direito no download de logs escolhe o destino. Nomes repetidos recebem sufixo; tarefas ativas podem ser canceladas, inclusive de janelas de logs separadas.
- Clique em uma linha concluída para abrir o arquivo. Abrir o arquivo ou mostrar sua pasta com sucesso fecha o painel. Posição, altura das linhas e rodapé dos logs permanecem estáveis durante o progresso.
- O progresso da conexão não desloca mais ícone e título. YAML abre sem um quadro de texto simples, pré-carrega o editor na intenção de abri-lo e reutiliza a instância ao trocar de recurso; rascunhos continuam protegidos.
