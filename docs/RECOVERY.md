# Recuperação e DR

## Recuperação local implementada

Após Ctrl+C, SIGTERM ou encerramento abrupto do processo principal, executar novamente `agentos run --state <diretório>` carrega a missão existente. Estados pausados, cancelados e concluídos não executam ciclos automaticamente.

Se houver um ciclo pendente, ele será repetido com o mesmo identificador. Isso é adequado ao exemplo de leitura, não a uma publicação ou pagamento sem idempotência.

## Exercício de restauração

1. Parar o executor e garantir que não há processo em execução.
2. Copiar o diretório de estado completo, incluindo `artifacts/`, para um destino novo.
3. Preservar o código e o workspace de entrada.
4. Iniciar `agentos run --state <novo-diretório>` no mesmo ambiente.
5. Comparar agent_id, contador de ciclos e memória.

O teste automatizado faz esta cópia e retoma o processo. É uma prova de restauração local, não de DR entre máquinas.

## DR futuro

Backup precisa incluir snapshot, artefatos, código, dependências Python e workspace. Os caminhos absolutos e o ambiente atestado precisam ser preservados ou migrados por procedimento administrativo validado em uma máquina nova. Revalidar permissões, suspensão e recibos externos antes de executar. A v0.2 bloqueia execução se script ou fingerprint do ambiente divergirem. `attest` só é permitido sem ciclo pendente e representa aceitação explícita do operador; não é recuperação automática.

Guardar cópias fora do servidor principal, proteger contra exclusão, verificar integridade e medir RPO/RTO por restauração real. Ainda não existe exportação assinada, migração de caminhos, backup automático, supervisor externo ou teste regional.

Um supervisor externo autorizado é necessário para reconstruir o ambiente perdido. O agente não consegue se recuperar da perda física de toda sua infraestrutura sem esse componente.
