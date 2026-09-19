# Changelog

## 0.4.0

- Adaptador Go opcional para Chat Completions, sem bibliotecas externas.
- Snapshot de fontes, trechos e hashes preservados nos artefatos cognitivos.
- Validação de esquema e citações literais; interpretações continuam não verificadas.
- Orçamento persistente de tentativas, timeout, resposta limitada e rejeição de ferramentas/redirecionamentos.
- Testes com provedor simulado, Python real e retomada após esgotamento.

Limites: análise somente; sem planejamento de ações, RAG, MCP ou avaliação com modelo real.

## 0.3.0

- Executor Docker padrão para novas missões; ID de imagem imutável e sem fallback.
- Raiz e entradas read-only, rede desabilitada, UID não privilegiado e limits cgroup verificados.
- Exportação de entradas sem links, sockets, arquivos ocultos ou subdiretórios.
- Broker Unix por execução com context.get e memory.read, SDK Python incorporado.
- Job CI dedicado a isolamento real, excesso de memória e timeout.
- Modo legado exige escolha explícita `--executor trusted-host` na criação/atestação.

Limites: kernel compartilhado, daemon e imagem confiáveis; sem microVM, MCP, LLM ou DR externo.

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
