# Arquitetura v0.7

O executor padrão de novas missões é Docker. As descrições de subprocesso Python abaixo se aplicam ao modo legado `trusted-host`; consulte [SANDBOX.md](SANDBOX.md) para a fronteira de isolamento, montagem de dados e broker. O controlador seleciona o executor pelo manifesto persistido; runtimes desconhecidos falham sem fallback.

## Componentes

`cmd/agentos`: CLI init/run/serve/status/pause/resume/cancel/attest.

`internal/core`: protocolo, estado, persistência, executor, manifesto, artefatos e controlador HTTP.

`CognitiveExecutor` envolve o executor selecionado quando o estado contém configuração LLM. Exporta fontes, executa Python e chama o endpoint configurado pelo operador. A reserva de tentativa é persistida antes desse trabalho. A memória inclui análise, citações verificadas e trechos com hashes; o modelo não recebe capacidades de ação. Veja [COGNITION.md](COGNITION.md).

Quando `retrieval` está configurado, o adaptador usa BM25 para selecionar trechos do snapshot local e de URIs MCP autorizadas. O cliente MCP roda no Go, fora do container sem rede, com credencial separada; não habilita acesso de rede ao script. O agendador e a máquina de estados permanecem iguais. Veja [RETRIEVAL.md](RETRIEVAL.md) para o perfil HTTP restrito, limites e comportamento em falhas.

## Controle concorrente

A v0.6 adiciona uma fila limitada de intenções, processada antes dos ciclos analíticos. Propostas são explícitas do operador e exigem aprovação do digest. O estado `in_flight` precede a rede; em recuperação, permite somente consulta ao destino. Recibos são persistidos mesmo após pausa/cancelamento, pois um efeito externo não pode ser descartado como um resultado de leitura. Veja [WRITES.md](WRITES.md).

Um mutex serializa alterações e snapshots, nunca o trabalho Python ou HTTP. Nos ciclos analíticos, o controlador persiste a intenção, libera o mutex e executa. Ao terminar, verifica status e geração da execução antes de confirmar. Pause/resume/cancel incrementam a geração e cancelam o contexto atual. Um resultado analítico antigo não confirma após uma mudança de autoridade. Ações de escrita mantêm o registro de efeitos conforme a regra específica acima.

Somente o serviço possui o lock de filesystem. Clientes HTTP não escrevem o snapshot diretamente. Erro de persistência bloqueia novas execuções; a API não confirma sucesso da alteração.

Bearer token exigido em todos os endpoints, comparação de digests em tempo constante, loopback obrigatório, limites de cabeçalhos e timeouts HTTP. O token não é repassado ao ambiente Python. Não existe TLS, RBAC ou autenticação de múltiplos usuários.

`examples/analyze.py`: script confiável, leitura de até 1000 entradas no primeiro nível do workspace, sem seguir links simbólicos na seleção de arquivos.

## Persistência

O backup offline mantém o flock durante a captura do checkpoint e dos artefatos referenciados. Um TAR verificável inclui script e cópia limitada de entradas. Na restauração, dependências são gravadas em um destino novo antes da publicação de state.json; a missão fica protegida por revisão de recuperação. Ações aprovadas/incertas retornam como unknown para consultar o destino, sem reenvio automático. Supervisor e agendamento de backup ficam no systemd, fora do núcleo. Veja [RECOVERY.md](RECOVERY.md).

Um diretório de estado por missão. O arquivo `lock` recebe flock exclusivo não bloqueante; o kernel libera o lock quando o processo fecha ou morre. Não remover o arquivo de lock durante execução. Somente filesystem local Linux é suportado; compartilhamento NFS não é validado.

O snapshot é escrito em arquivo temporário no mesmo diretório, sincronizado com fsync, renomeado e seguido por fsync do diretório. JSON inválido, versão desconhecida e status inválido causam erro; não ocorre reset silencioso. Campos novos são aditivos ao esquema 1. Estados antigos precisam de atestação explícita antes de executar Python.

Estado inicial: identidade aleatória, protocolo, status ativo e próxima execução. Antes de iniciar Python, gravamos `pending` e evento `started`. Depois de validar JSON, gravamos memória, contador, evento de conclusão e próximo horário em um único snapshot.

Se houver falha após iniciar Python e antes do snapshot final, a atividade será repetida com o mesmo cycle_id. Nenhuma garantia exactly-once para efeitos externos. O exemplo não faz escritas externas.

## Execução Python

Requisição JSON em stdin: agent_id, mission, cycle_id, workspace, memory.

Resposta: objeto JSON em stdout, limitado a 1 MiB. Resultado inválido não confirma o ciclo. O stderr também é limitado e não é incluído na mensagem de erro do núcleo. O resultado é escrito atomicamente em arquivo endereçado pelo SHA-256 antes de o snapshot confirmar a referência. Uma falha intermediária pode deixar um arquivo órfão, nunca uma confirmação anterior à persistência do artefato.

O executor host utiliza grupo de processos Linux, timeout e encerramento do grupo em cancelamento. Isso não é uma barreira contra código malicioso que crie sessões próprias. O modo Docker aplica a fronteira adicional descrita em SANDBOX.md.

## Operação

Uma falha no script encerra `run` com erro e preserva o ciclo pendente. Em `serve`, pausa a missão, mantendo a API disponível. Não existe retry infinito automático nesta versão. O intervalo é calculado após a conclusão; ativações perdidas não são acumuladas.

O máximo de ciclos limita execuções confirmadas. Missões cognitivas também têm limite persistente de tentativas, consumido inclusive em falhas. Isso não constitui orçamento financeiro global: cobrança e limites monetários precisam ser configurados no provedor.

## Segurança e confiança

Operador, protocolo, scripts e dependências são confiáveis nesta fase. Diretório de estado criado com 0700, snapshots e artefatos com 0600; isso não constitui criptografia. A identidade aleatória é um identificador, não uma assinatura criptográfica. O manifesto confere script, executável Python e inventário de pacotes, mas não todos os bytes do ambiente. O script principal é executado a partir dos bytes verificados; a substituição concorrente de dependências/interpreter pelo operador não está no modelo de proteção.

A v0.3 adiciona sandbox Docker e broker de snapshots de leitura. Identidade de workload, autorização de ações externas e homologação para dados sensíveis continuam pendentes.
