# Changelog

## 0.2.0

- Serviço HTTP loopback com token obrigatório e controle durante execução.
- Pausa/cancelamento do executor e rejeição de resultados de gerações anteriores.
- Histórico de resultados por ciclo, SHA-256, persistência atômica e verificação na leitura.
- Manifesto do script, executável Python e inventário de ambiente.
- Atestação explícita para estados antigos sem trabalho pendente.
- Testes de corrida, autenticação, integridade e ciclo real pela API.

Limites: scripts confiáveis; sem sandbox, TLS, RBAC, LLM, MCP ou DR remoto.

## 0.1.0

- Missão persistente, ciclos determinísticos, Python separado e snapshot local.
- Testes de interrupção e restauração em outro diretório.
