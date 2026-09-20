# MCP de leitura e RAG — fase 0.5

O AgentOS recupera trechos do workspace e de recursos MCP explicitamente autorizados, classifica sua relevância para a missão e envia até oito trechos ao modelo. O protocolo continua determinístico: o modelo analisa o material recebido, sem descobrir servidores, escolher URIs ou chamar ferramentas.

## Executar

Para usar somente fontes locais, acrescente `--rag` às opções de uma nova missão com LLM. Para incluir recursos remotos, configure um endpoint e repita `--mcp-resource` para cada URI autorizada. Essas opções também habilitam RAG:

```bash
./agentos init --state state/replicacao \
  --image "$AGENTOS_IMAGE" \
  --workspace examples/data --script examples/analyze.py \
  --mission "replicação backup recuperação" --cycles 3 \
  --llm-base-url https://SEU-PROVEDOR/v1 --llm-model SEU-MODELO \
  --llm-max-calls 3 --rag \
  --mcp-endpoint https://SEU-SERVIDOR/mcp \
  --mcp-resource 'reports://replication/latest' \
  --mcp-resource 'reports://backup/latest'
./agentos run --state state/replicacao
```

Substitua servidor, provedor, modelo e URIs pelos valores reais. O ID Docker deve estar definido conforme o README. Configure `AGENTOS_LLM_API_KEY` e, quando necessário, `AGENTOS_MCP_TOKEN` no ambiente de `run` ou `serve`. São tokens separados, enviados apenas ao respectivo endpoint e ausentes do ambiente Python e do snapshot. URLs e URIs ficam no estado/artefatos; não coloque segredos nesses campos. Não há fluxo OAuth nem renovação automática de token.

O MCP e o RAG exigem cognição habilitada. A configuração é persistida no `init`; alterar flags em `run` não reconfigura a missão. Estados anteriores permanecem no comportamento da v0.4 até criação explícita de uma nova missão com essas opções.

## Recuperação lexical

1. O núcleo captura o workspace com os mesmos limites de isolamento: primeiro nível, até 1.000 entradas, arquivos regulares não ocultos, sem links; 4 MiB por arquivo e 16 MiB no conjunto.
2. Seleciona documentos UTF-8 `.txt`, `.md`, `.csv` e `.json`, sem bytes nulos. Pode usar apenas recursos MCP quando o workspace não contém textos elegíveis.
3. Lê uma vez cada URI autorizada e acrescenta os documentos remotos ao corpus. Uma falha remota encerra a tentativa; não continua silenciosamente com fontes parciais ou antigas.
4. Divide documentos em trechos de até 2.048 bytes com sobreposição de aproximadamente 256 bytes, respeitando limites de caracteres UTF-8. Limite de 12.000 trechos candidatos.
5. Usa a missão como consulta e classifica por BM25 (`k1=1.2`, `b=0.75`). A consulta aceita até 8 KiB e 128 termos distintos. Termos são letras e números Unicode em minúsculas; não há stemming, sinônimos, remoção de acentos ou de palavras comuns.
6. Envia ao modelo até oito trechos com pontuação positiva. Empates usam IDs estáveis, inclusive com soma de pontuações em ordem determinística. Sem correspondência lexical, falha antes de Python/LLM; refine a missão ou as fontes.

É RAG lexical: há recuperação seguida de geração com evidências. Não é busca semântica, não usa embeddings ou banco vetorial. Pontuação positiva não garante pertinência; consultas longas com termos genéricos podem recuperar ruído. A qualidade precisa ser avaliada no corpus de uso.

O índice é reconstruído em memória a cada ciclo. Ele consulta os documentos atuais, não o histórico de artefatos. A memória anterior continua no contexto conforme o limite da fase 0.4. Documentos MCP não são escritos no workspace nem disponibilizados ao Python como arquivos; os trechos confirmados poderão aparecer na memória do próximo ciclo.

## Perfil MCP implementado

Cliente de recursos via Streamable HTTP, fixado no protocolo `2025-06-18`. Segue a [inicialização MCP](https://modelcontextprotocol.io/specification/2025-06-18/basic/lifecycle), usa [recursos identificados por URI](https://modelcontextprotocol.io/specification/2025-06-18/server/resources) e recebe respostas JSON ou SSE conforme o [transporte HTTP](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports).

| Aspecto | Contrato desta versão |
|---|---|
| Escopo | Um endpoint e de 1 a 8 URIs exatas, sem duplicatas |
| Ciclo | `initialize`, `notifications/initialized`, `resources/read` por URI |
| Sessão | Repassa `Mcp-Session-Id` e versão negociada; tenta DELETE da sessão ao encerrar |
| Conteúdo | Exatamente um conteúdo textual por URI; até 256 KiB de texto por recurso |
| Validação | Versão e capacidade `resources`, JSON-RPC 2.0, ID correspondente e URI exatamente autorizada |
| HTTP | HTTPS ou HTTP loopback literal; sem redirecionamentos ou proxy de ambiente |
| Limites | 2 MiB por resposta/stream, até 32 eventos SSE, 10 s por POST, 30 s para aquisição completa e até 2 s adicionais de encerramento |
| Credenciais | Bearer pré-configurado; sem OAuth, descoberta ou aquisição automática de privilégios |

Notificações recebidas são ignoradas dentro desses limites. Requisições iniciadas pelo servidor, inclusive sampling/elicitation/ping, encerram a tentativa neste perfil restrito. Não há GET SSE permanente, retomada de eventos, transporte stdio, protocolo HTTP+SSE antigo, templates de recursos, paginação ou subscriptions. Versões incompatíveis são rejeitadas.

HTTP 404 de sessão expirada encerra a tentativa. Uma nova tentativa começa outra sessão por `initialize`, consumindo outra reserva persistente; não há reabertura ou retry transparente durante o mesmo ciclo. Esse comportamento conservador e a recusa de requisições do servidor limitam a interoperabilidade; não se trata de um cliente MCP universal.

Não implementa `tools/list` nem `tools/call`: servidores que oferecem consultas somente como ferramentas precisam expor recursos ou aguardar um adaptador de ferramentas com política própria. Anotações como `readOnlyHint` não são usadas como autorização. Uma URI recebida é um identificador para o servidor MCP, nunca um caminho que o AgentOS abre nem uma URL que ele segue.

O cliente restringe os métodos que envia; não consegue provar que a implementação remota de `resources/read` é livre de efeitos colaterais. O operador deve escolher um servidor confiável. Textos recebidos continuam dados não confiáveis e não podem ampliar permissões do agente.

## Evidências e recuperação após falha

Cada trecho confirmado registra origem (`workspace` ou `mcp`), nome/URI, endpoint quando remoto, hash do documento, hash do trecho, ID do documento, posições em bytes e pontuação. `start_byte` ausente significa zero; `end_byte` é exclusivo. Os IDs incluem a identidade da origem, o hash do documento e a faixa do trecho, evitando confundir versões ou servidores diferentes.

O artefato preserva os trechos selecionados, as citações e o relatório `retrieval` com consulta, método e contagens. Citações devem existir literalmente no trecho referenciado; conclusões permanecem não verificadas. O corpus completo não fica arquivado e um hash não permite reconstruí-lo. Índice e metadados não são uma trilha assinada.

A reserva de `model_attempts` ocorre antes da leitura MCP. Falhas de recuperação também consomem orçamento mesmo sem chamada ao modelo. Cada reserva permite no máximo uma inicialização, uma notificação, oito leituras e uma tentativa de encerrar sessão; não há loops de descoberta. Reiniciar ou retomar não repõe orçamento. `run` encerra com erro; `serve` pausa, mantendo o ciclo pendente e a última memória confirmada.

Em replay, o mesmo ID de ciclo pode capturar novas versões das fontes. Não há snapshot distribuído consistente entre workspace e servidor MCP. Para DR, preserve estado e artefatos, fontes ou versões recuperáveis, configuração dos servidores e credenciais por canal separado. Backup externo e restauração assistida continuam previstos na fase 0.7.

## Testar

```bash
go test -race ./...
go vet ./...
go build -o agentos ./cmd/agentos
python3 -m unittest discover -s tests -p test_retrieval.py -v
```

O teste de integração usa servidores MCP e LLM simulados, Python real, uma queda remota após o primeiro ciclo e reinício com orçamento esgotado. Com `AGENTOS_SANDBOX_IMAGE` definido, executa o mesmo fluxo em Docker; o CI faz essa validação. Os testes Go verificam JSON/SSE, sessão, escopo, cancelamento, limites e ranking de trechos posteriores. Não houve homologação com servidor MCP de terceiros nem avaliação com modelo real.
