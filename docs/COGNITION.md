# Cognição e memória — fase 0.4

O micronúcleo continua determinístico. Um adaptador Go opcional chama uma API compatível com Chat Completions depois que o Python termina. A resposta é uma análise estruturada; o modelo não executa ferramentas, altera permissões, escreve scripts ou muda seu agendamento. A missão orienta a análise, enquanto o protocolo mantém o controle operacional.

## Habilitar

Na criação de uma missão, acrescente estas opções ao comando `init` do README:

```bash
--llm-base-url https://SEU-SERVIDOR/v1 \
--llm-model SEU-MODELO \
--llm-max-calls 3 \
--llm-max-tokens 1024
```

Defina `AGENTOS_LLM_API_KEY` no ambiente de `run` ou `serve` se o provedor exigir autenticação. A chave é lida pelo Go, não é persistida e não é passada ao ambiente Python. Configure HTTPS; para um servidor local, é permitido HTTP em endereço loopback literal, como `http://127.0.0.1:8000/v1`. Não há redirecionamentos nem proxy de ambiente. Endpoint e modelo são escolhas do operador na inicialização, nunca da saída do script.

A integração envia ao endpoint a missão, os trechos selecionados, o resultado Python e a memória anterior quando couber. Habilite-a apenas para fontes que possam ser enviadas ao provedor escolhido. Sem essas opções, permanece a execução sem modelo. Estados anteriores continuam compatíveis, sem ativação automática de cognição.

Contrato: POST `<base-url>/chat/completions`, campos `model`, `messages`, `max_tokens` e `stream: false`. Exige uma escolha com `finish_reason: stop`, conteúdo JSON e nenhuma chamada de ferramenta. Provedores com outro contrato precisam de adaptação explícita. Testes usam um servidor simulado; qualidade e compatibilidade com um modelo real ainda precisam de avaliação.

## Fontes e confirmação

Antes do script, o adaptador exporta um snapshot limitado pelas regras de SANDBOX.md. O script recebe essa cópia. Sem RAG, a seleção lê os primeiros oito arquivos elegíveis em ordem de nome, no primeiro nível: `.txt`, `.md`, `.csv` e `.json`, UTF-8 sem bytes nulos. Cada trecho contém até 2.048 bytes, respeitando caracteres completos. A v0.5 adiciona recuperação lexical opcional e fontes MCP; veja [RETRIEVAL.md](RETRIEVAL.md).

Cada fonte registra nome, ID derivado de nome e conteúdo, hash SHA-256 completo, hash do trecho, texto, indicador de truncamento e data de captura. Os trechos ficam dentro do artefato, permitindo conferir as citações depois que os arquivos originais mudarem. O conteúdo completo de arquivos truncados não fica arquivado; seu hash sozinho não permite reconstruí-los. Um replay captura novamente os arquivos e pode observar outra versão.

A análise contém `summary` e de uma a oito `claims`, cada uma com `text`, `source_id` e `quote`. O núcleo rejeita campos extras, fontes desconhecidas e citações que não sejam substrings exatas do trecho correspondente. Uma fonte maliciosa ainda pode influenciar a análise; o isolamento impede que essa resposta adquira capacidades operacionais.

O resultado confirmado usa `agentos.cognitive-memory.v1` e `evidence_status: quotes_verified_claims_unverified`. Verificar uma citação comprova sua presença na fonte, não que ela sustente a conclusão ou seja verdadeira. O resumo também é interpretação não verificada. A memória não deve ser usada como autorização para ações. A saída Python original fica no campo `execution`.

## Orçamento, falhas e recuperação

Cada tentativa cognitiva reserva uma unidade de `model_attempts` no snapshot **antes** de executar Python ou chamar o modelo. Falhas anteriores à chamada também consomem a reserva: é uma escolha conservadora contra repetições ilimitadas após crash. Reiniciar ou usar `resume` não repõe esse orçamento. Ao esgotá-lo, a missão pausa sem chamar o executor. Uma nova missão exige outro estado.

O limite de chamadas é de 1 a 10.000; o pedido de saída é de 128 a 4.096 tokens. Isso não é teto financeiro: tokens de entrada, cobrança e cumprimento do limite dependem do provedor. Não há retry HTTP implementado nem streaming. Timeout da chamada: 20 segundos, além do timeout do Python. Resposta HTTP máxima: 512 KiB; resultado Python enviado: 32 KiB; memória anterior: incluída até 16 KiB, omitida acima disso.

Saída inválida, falta de fontes ou falha do provedor não substituem a memória confirmada. `run` termina com erro; `serve` pausa a missão. O ciclo pendente mantém seu identificador. Um crash após envio pode ter sido cobrado pelo provedor mesmo sem confirmação local; replay pode gerar outra chamada, dentro das reservas restantes. Não existe garantia de execução única no provedor.

O artefato e o checkpoint seguem a persistência atômica existente. Restaurar um backup antigo também restaura um orçamento antigo: controle de custos global requer um limite externo no provedor. Não há DR externo nem memória vetorial. A fase 0.5 implementa MCP de recursos e recuperação lexical opcional.

## Validação reproduzível

```bash
go test -race ./...
go build -o agentos ./cmd/agentos
python3 -m unittest discover -s tests -p test_cognition.py -v
```

O teste completo cria um provedor HTTP local determinístico, executa o Python real, confere a memória persistida e reinicia após esgotamento. Não requer chave real nem chamadas pagas.
