# SPK Ocular 1.8.0

- Edite valores individuais de ConfigMap com realce de sintaxe, pesquisa e desfazer. A revisão mostra a alteração exata em relação ao valor atual; entradas binárias e conteúdo de Secret mantêm sua apresentação protegida.
- Pesquise recursos com fragmentos explícitos de namespace, tipo e nome. Namespaces ambíguos oferecem uma escolha; um resultado atual único abre o objeto exato preservando a proteção das alterações não salvas.
- Os grupos de navegação têm ícones e espaçamento consistentes. Recolher um grupo oculta todos os filhos, inclusive o recurso selecionado, sem fechar a página aberta.
- Espaços desconectados mostram uma prévia estática do provedor, Connect/Cancel junto ao estado e ilustrações transparentes separadas de farol e montanhas. O farol desenhado e seu feixe suave mantêm tamanho fixo e são recortados quando a largura do painel muda.
- Um único rodapé mostra o estado da visualização ativa. Os detalhes de atualização aparecem após dois segundos sem deslocar os controles; foco e redimensionamento de painéis aninhados não movem a estrutura da aplicação.
- Alt+Shift+setas redimensiona o painel em foco. Alt+Shift+Enter destaca ou devolve os logs mantendo um único fluxo; novas janelas aproveitam mais a área útil da tela atual.
- Os menus do painel inferior fecham todas as abas ou as abas à esquerda ou à direita, respeitando abas fixadas e sua vinculação.
- Os detalhes dos recursos têm tipografia mais clara e tabelas compactas de revisões e Events consistentes. Events mantém ordenação, ajuste de colunas e Refresh; as relações de Ingress com serviços mostram cada caminho declarado.
- O texto do terminal já enviado não é mais repetido durante substituições por IME. A composição ativa, a colagem Unicode e os buffers de anúncio do leitor de tela continuam protegidos.
