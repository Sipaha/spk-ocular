# SPK Ocular 1.6.2

- Ao redimensionar o painel de logs, as linhas visíveis mantêm sua posição na tela. Follow permanece na linha mais recente; consultar o histórico não reativa Follow. Reduzir, ampliar, cancelar o redimensionamento e reabrir uma aba oculta preservam a posição dentro do intervalo de rolagem disponível.
- Ativar ou desativar Wrap mantém a linha mais recente visível quando Follow está ativo, inclusive ao alterar Wrap durante o redimensionamento.
- Nova configuração desbloqueia diretamente o armazenamento local criptografado com a senha mestra e abre o editor YAML, sem exigir Connect nem iniciar uma conexão com o cluster. Isso também funciona quando já existe uma senha mestra, mas nenhuma configuração criptografada foi salva, ou a última foi removida.
- Cancelar a entrada da senha não altera o armazenamento. Connect continua desbloqueando e prosseguindo apenas com a conexão selecionada explicitamente. A explicação para criar uma configuração está disponível nos oito idiomas do aplicativo.
