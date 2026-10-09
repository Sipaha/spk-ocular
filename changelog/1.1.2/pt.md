# SPK Ocular 1.1.2

- Compilação com Go 1.26.9 e golang.org/x/net v0.60.0 para corrigir as vulnerabilidades HTTP/2 recentemente divulgadas.
- Explore arquivos de contêineres Kubernetes e Docker: árvore de pastas em cache, destinos de links simbólicos, destaque de sintaxe e edição protegida de UTF-8 até 2 MiB. O aplicativo desktop copia arquivos ou pastas inteiros por um seletor nativo de diretório, sem sobrescrever nomes existentes.
- Abra logs em uma janela nativa separada mantendo um único fluxo. Escolha o destino das exportações em texto. Todos os níveis ficam visíveis; filtros de texto e pesquisa continuam disponíveis.
- Escolha um Pod para workloads com várias instâncias e um contêiner para Pods com vários contêineres. O botão direito em Logs, Terminal ou Files abre a configuração completa; somente Terminal inclui um campo de comando. Uma opção única abre diretamente com o botão esquerdo e permanece visível na configuração.
- Consulte revisões preservadas de Deployment e compare o YAML do modelo do Pod, somente leitura, com o Deployment atual ou outra revisão.
- Atalhos seguem as teclas físicas independentemente do layout, inclusive no editor. Corrigidos caracteres cirílicos repetidos, atualizações intermediárias de tamanho do terminal e o foco que permanecia no separador.
- Mais espaço no inspetor, árvore redimensionável, contagens em cache e linhas estáveis de carregamento/pastas vazias. Erros não deslocam o conteúdo. Recursos relacionados em linhas compactas e ações de janela como ícones à direita.

Operações de arquivos exigem um contêiner Linux em execução com sh, readlink para links e tar para downloads. Downloads nativos não têm o limite de 2 MiB do editor e exigem o aplicativo desktop. Entradas inseguras do arquivo e colisões de nomes são recusadas.

Pacotes nativos para Linux, Windows e macOS em amd64 e arm64. O estado de assinatura/notarização não muda e arquivos de versões anteriores não são reescritos.
