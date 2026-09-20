# Visão do projeto

## Objetivo

Construir um Agent Operations System simples no núcleo e extensível nas capacidades. O agente deve seguir um protocolo persistente sem receber novos prompts a cada ciclo, preservar compromissos e recuperar trabalho após falhas.

Chamamos continuidade à preservação computacional de identidade, missão, memória e estado; não a uma alegação de consciência ou senciência.

## Princípios

1. O estado representa o agente; processos podem ser substituídos.
2. O núcleo aplica regras determinísticas. Modelos propõem ações, não concedem autoridade.
3. Python é a linguagem de trabalho; Go é o núcleo e a supervisão.
4. Autonomia tem objetivo, orçamento e critérios de parada.
5. Suspensão e cancelamento não podem ser revertidos por scripts.
6. Recuperar corretamente é prioritário em relação a disponibilidade ininterrupta.
7. Resultado externo desconhecido exige reconciliação.
8. Configuração, código, dados e dependências precisam ser recuperáveis.

## Protocolo autônomo

Despertar → carregar estado → verificar autoridade e orçamento → executar atividade → validar saída → confirmar memória e ciclo → agendar próxima ativação.

O protocolo inicial é uma máquina de estados explícita. A v0.4 interpreta objetivos para análise com fontes. A seleção de tarefas permanece futura, preservando a obrigação de persistir e verificar cada resultado.

## Arquitetura de produto

Núcleo: identidade, estado, agenda, controle e interface de capacidades.

Executores: ambientes Python isoláveis, eventualmente outros runtimes.

Extensões: provedores LLM, RAG, MCP, artefatos, consultas e análise de sistemas.

Persistência: interfaces exportáveis e versões de esquema explícitas. O snapshot JSON local é uma decisão de laboratório, não uma escolha definitiva para produção.

## Entregas

| Marco | Conteúdo | Situação |
|---|---|---|
| 0.1 | Missão única, ciclos, Python, snapshot, retomada e testes | Implementado |
| 0.2 | API de controle, histórico de artefatos, verificação de script e ambiente | Implementado; limites descritos no README |
| 0.3 | Executor Docker restrito, limites efetivos e broker de leitura | Implementado; requer validação no host de implantação |
| 0.4 | Adaptador LLM e memória recuperável com fontes | Implementado; validado com provedor simulado |
| 0.5 | MCP de leitura e RAG | Implementado: recursos HTTP autorizados e BM25; perfil e limites em RETRIEVAL.md |
| 0.6 | Intenções, aprovações, recibos e reconciliação de escrita | Implementado para record.create; contrato do destino e limites em WRITES.md |
| 0.7 | Backup consistente, restauração assistida e supervisor externo | Implementado: TAR verificável, proteção na retomada e units systemd; destino externo/runtime exigem configuração |

Temporal permanece uma alternativa para workflows mais complexos. A primeira versão deliberadamente demonstra um protocolo pequeno com snapshot; não reimplementa um motor distribuído de workflows.

## Critério de sucesso inicial

Dar uma missão uma única vez, completar ciclos, encerrar abruptamente o processo e retomar sem alterar identidade nem repetir ciclos já confirmados. Reexecução de ciclo não confirmado usa o mesmo identificador.

## Independência

O agente deve depender de contratos substituíveis para modelos e ferramentas. Ainda depende fisicamente de energia, computação, armazenamento e um supervisor que exista fora de qualquer ambiente perdido. Não pretende burlar suspensão, ampliar suas permissões ou reconstruir infraestrutura sem autorização.
