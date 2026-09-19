# Arquitetura v0.2

## Componentes

`cmd/agentos`: CLI init/run/serve/status/pause/resume/cancel/attest.

`internal/core`: protocolo, estado, persistência, executor, manifesto, artefatos e controlador HTTP.

## Controle concorrente

Um mutex serializa alterações e snapshots, nunca o trabalho Python. O controlador persiste a intenção, libera o mutex e executa. Ao terminar, verifica status e geração da execução antes de confirmar. Pause/resume/cancel incrementam a geração e cancelam o contexto atual. Um resultado antigo não confirma após uma mudança de autoridade.

Somente o serviço possui o lock de filesystem. Clientes HTTP não escrevem o snapshot diretamente. Erro de persistência bloqueia novas execuções; a API não confirma sucesso da alteração.

Bearer token exigido em todos os endpoints, comparação de digests em tempo constante, loopback obrigatório, limites de cabeçalhos e timeouts HTTP. O token não é repassado ao ambiente Python. Não existe TLS, RBAC ou autenticação de múltiplos usuários.

`examples/analyze.py`: script confiável, leitura de até 1000 entradas no primeiro nível do workspace, sem seguir links simbólicos na seleção de arquivos.

## Persistência

Um diretório de estado por missão. O arquivo `lock` recebe flock exclusivo não bloqueante; o kernel libera o lock quando o processo fecha ou morre. Não remover o arquivo de lock durante execução. Somente filesystem local Linux é suportado; compartilhamento NFS não é validado.

O snapshot é escrito em arquivo temporário no mesmo diretório, sincronizado com fsync, renomeado e seguido por fsync do diretório. JSON inválido, versão desconhecida e status inválido causam erro; não ocorre reset silencioso. Campos novos são aditivos ao esquema 1. Estados antigos precisam de atestação explícita antes de executar Python.

Estado inicial: identidade aleatória, protocolo, status ativo e próxima execução. Antes de iniciar Python, gravamos `pending` e evento `started`. Depois de validar JSON, gravamos memória, contador, evento de conclusão e próximo horário em um único snapshot.

Se houver falha após iniciar Python e antes do snapshot final, a atividade será repetida com o mesmo cycle_id. Nenhuma garantia exactly-once para efeitos externos. O exemplo não faz escritas externas.

## Execução Python

Requisição JSON em stdin: agent_id, mission, cycle_id, workspace, memory.

Resposta: objeto JSON em stdout, limitado a 1 MiB. Resultado inválido não confirma o ciclo. O stderr também é limitado e não é incluído na mensagem de erro do núcleo. O resultado é escrito atomicamente em arquivo endereçado pelo SHA-256 antes de o snapshot confirmar a referência. Uma falha intermediária pode deixar um arquivo órfão, nunca uma confirmação anterior à persistência do artefato.

O executor utiliza grupo de processos Linux, timeout e encerramento do grupo em cancelamento. Isso não é uma barreira contra código malicioso que crie sessões próprias. Filhos independentes e acesso ao host requerem sandbox real na próxima etapa.

## Operação

Uma falha no script encerra `run` com erro e preserva o ciclo pendente. Em `serve`, pausa a missão, mantendo a API disponível. Não existe retry infinito automático nesta versão. O intervalo é calculado após a conclusão; ativações perdidas não são acumuladas.

O máximo de ciclos é um limite de execuções confirmadas, não de tentativas. Limites persistentes de tentativas e orçamento financeiro serão necessários antes de habilitar ferramentas pagas.

## Segurança e confiança

Operador, protocolo, scripts e dependências são confiáveis nesta fase. Diretório de estado criado com 0700, snapshots e artefatos com 0600; isso não constitui criptografia. A identidade aleatória é um identificador, não uma assinatura criptográfica. O manifesto confere script, executável Python e inventário de pacotes, mas não todos os bytes do ambiente. O script principal é executado a partir dos bytes verificados; a substituição concorrente de dependências/interpreter pelo operador não está no modelo de proteção.

As camadas futuras de broker, identidade de workload e sandbox devem ser implementadas antes de executar código gerado por LLM com acesso a dados ou redes sensíveis.
