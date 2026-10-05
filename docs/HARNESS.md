# Agent Harness — v0.11

A v0.11 inicia a transformação do AgentOS em um sistema de confiança para agentes de IA.

O Harness é uma camada determinística do núcleo. Modelos podem propor especificações, planos, resultados e revisões, mas não controlam transições, permissões ou aprovação.

## Fluxo

```text
MISSION -> SPEC -> PLAN -> TASK -> BUILD -> REVIEW -> VERIFY -> APPROVE -> COMPLETE
```

Uma reprovação pode devolver a tarefa para execução ou encerrá-la como rejeitada. Uma tarefa concluída é terminal.

## Princípios

1. O Kernel mantém a autoridade.
2. Modelos são componentes substituíveis.
3. Planos possuem dependências explícitas.
4. Resultados precisam ser identificáveis e vinculados à tarefa.
5. Revisão não equivale a verificação.
6. Aprovação humana ou de política é uma etapa explícita.
7. O Harness não concede ferramentas diretamente ao modelo.

## Primeira entrega

O pacote `internal/harness` contém os contratos puros e a máquina de estados da v0.11. Ele deliberadamente não possui HTTP, SDK de LLM, filesystem, MCP ou execução de comandos. Essa separação permite testar as regras de autoridade antes de conectar provedores e executores.

Próximas etapas da v0.11: persistência do estado do Harness; executor de tarefas; adaptadores de agentes; verificador; integração com o mecanismo existente de aprovação; CLI e API.
