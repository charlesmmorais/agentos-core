# Arquitetura v0.1

## Componentes

`cmd/agentos`: CLI init/run/status/pause/resume/cancel.

`internal/core`: protocolo, estado, persistência e executor.

`examples/analyze.py`: script confiável, leitura de até 1000 entradas no primeiro nível do workspace, sem seguir links simbólicos na seleção de arquivos.

## Persistência

Um diretório de estado por missão. O arquivo `lock` recebe flock exclusivo não bloqueante; o kernel libera o lock quando o processo fecha ou morre. Não remover o arquivo de lock durante execução. Somente filesystem local Linux é suportado; compartilhamento NFS não é validado.

O snapshot é escrito em arquivo temporário no mesmo diretório, sincronizado com fsync, renomeado e seguido por fsync do diretório. JSON inválido, versão desconhecida e status inválido causam erro; não ocorre reset silencioso.

Estado inicial: identidade aleatória, protocolo, status ativo e próxima execução. Antes de iniciar Python, gravamos `pending` e evento `started`. Depois de validar JSON, gravamos memória, contador, evento de conclusão e próximo horário em um único snapshot.

Se houver falha após iniciar Python e antes do snapshot final, a atividade será repetida com o mesmo cycle_id. Nenhuma garantia exactly-once para efeitos externos. O exemplo não faz escritas externas.

## Execução Python

Requisição JSON em stdin: agent_id, mission, cycle_id, workspace, memory.

Resposta: objeto JSON em stdout, limitado a 1 MiB. Resultado inválido não confirma o ciclo. O stderr também é limitado e não é incluído na mensagem de erro do núcleo.

O executor utiliza grupo de processos Linux, timeout e encerramento do grupo em cancelamento. Isso não é uma barreira contra código malicioso que crie sessões próprias. Filhos independentes e acesso ao host requerem sandbox real na próxima etapa.

## Operação

Uma falha no script encerra `run` com erro e preserva o ciclo pendente. Um novo `run` tenta novamente. Não existe retry infinito automático nesta versão. O intervalo é calculado após a conclusão; ativações perdidas não são acumuladas.

O máximo de ciclos é um limite de execuções confirmadas, não de tentativas. Limites persistentes de tentativas e orçamento financeiro serão necessários antes de habilitar ferramentas pagas.

## Segurança e confiança

Operador, protocolo, scripts e dependências são confiáveis nesta fase. Diretório de estado criado com 0700, snapshots com 0600; isso não constitui criptografia. A identidade aleatória é um identificador, não uma assinatura criptográfica.

As camadas futuras de broker, identidade de workload e sandbox devem ser implementadas antes de executar código gerado por LLM com acesso a dados ou redes sensíveis.
