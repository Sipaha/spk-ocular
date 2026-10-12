# SPK Ocular 1.4.0

## Pesquisa e ferramentas rápidas

- Ctrl+K recupera consultas QWERTY em inglês digitadas com um layout russo ou ucraniano compatível quando a consulta original não encontra resultados. O texto, os escopos e a identidade do recurso são preservados; um aviso traduzido mostra a consulta recuperada.
- Ao abrir um objeto pela pesquisa, sua linha selecionada recebe o foco. Logs, terminal e arquivos de um Pod específico usam o contêiner padrão em execução ou o primeiro em execução. Os logs continuam disponíveis para Pods concluídos. Réplicas mantêm a escolha explícita do Pod; o botão direito abre a configuração completa.
- Abrir ou reabrir logs direciona o foco para sua área. Follow mostra End na dica e na referência do teclado; os campos de texto mantêm o comportamento normal de End.

## Interface, confiabilidade e desempenho

- Modais cobrem as barras de rolagem e divisórias do fundo. O foco da tabela inclui cabeçalho e linhas; os menus ficam acima do painel inferior e as dicas próximas aos botões.
- Diálogos de leitura e configuração compartilham o comportamento de foco, Tab e fechamento. A proteção de alterações não salvas foi preservada.
- O histórico do agente rejeita respostas antigas após mudanças de filtro, mantém destinos históricos e formata datas no idioma selecionado. Relações, revisões de Deployment e recursos Helm usam tabelas compactas e alinhadas.
- As solicitações HTTP da interface têm limites de tamanho e prazo de leitura. A verificação de identidade da sessão e o cancelamento de consultas de inventário em espera impedem leituras antigas em sessões substitutas. Permissões, escopos e limites dos provedores não mudam.
- A pesquisa regex em logs processa lotes incrementais sem uma segunda cópia do histórico no worker. Projeções Compose usam índices locais; linhas sem alterações e atualizações apenas de status evitam trabalho redundante.
- Os oito idiomas, o executável unificado desktop/browser e a verificação obrigatória das seis variantes de plataforma foram mantidos.
