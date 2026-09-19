# AgentOS Core

Micronúcleo em Go para agentes persistentes, com protocolo autônomo e executor Python separado.

**v0.2 — laboratório de continuidade local, Linux/WSL2.** API local autenticada, histórico de resultados por ciclo e verificação de script/ambiente. Sem dependências Go externas. Não inclui LLM, RAG, MCP, sandbox de segurança, HA ou DR automático nesta etapa.

## Visão

O agente é sua identidade, missão, protocolo, memória e compromissos persistidos. O processo é um executor substituível. Ele recebe a missão uma vez, executa ciclos agendados e retoma com a mesma identidade após reinício.

O núcleo controla estado, agendamento, limites e supervisão. Scripts realizam o trabalho. O exemplo Python observa arquivos locais sem modificá-los; sua saída JSON torna-se a memória do ciclo mais recente. O texto da missão é descritivo: esta versão não interpreta objetivos em linguagem natural.

Leia [a visão detalhada](docs/VISION.md), [a arquitetura](docs/ARCHITECTURE.md) e [a recuperação](docs/RECOVERY.md).

## Executar

Requisitos: Linux ou WSL2, Go 1.23+ e Python 3.10+. Sem chaves de IA ou serviços externos.

```bash
git clone https://github.com/charlesmmorais/agentos-core.git
cd agentos-core
go build -o agentos ./cmd/agentos
./agentos init --workspace examples/data --script examples/analyze.py --interval 5 --cycles 3 --mission "Acompanhar arquivos locais"
./agentos run
./agentos status
```

`init` registra caminhos absolutos e não sobrescreve estado existente. `run` encerra após o orçamento de ciclos. Para outra missão, use outro diretório: `--state state/outra-missao` em todos os comandos.

Para observar a continuidade, inicialize com mais ciclos, execute `run`, interrompa com Ctrl+C e execute `run` novamente. Não repita `init`. A identidade, os ciclos confirmados e a próxima ativação permanecem.

## Controle

O lock exclusivo protege todo o período de execução. Para os comandos CLI abaixo, pare `run` antes de consultar ou alterar o estado. Para controlar sem interromper o serviço, use `serve` e a API:

```bash
./agentos pause
./agentos run    # não executa enquanto pausado
./agentos resume
./agentos run
```

`cancel` encerra administrativamente a missão; cancelamento e conclusão são terminais. Ctrl+C apenas encerra o executor e permite retomada posterior.

## API durante a execução

Inicialize a missão primeiro. Em Linux/WSL2, gere um token local e mantenha-o no terminal:

```bash
export AGENTOS_API_TOKEN="$(python3 -c 'import secrets; print(secrets.token_hex(32))')"
./agentos serve --listen 127.0.0.1:8080 &
AGENTOS_PID=$!
curl -H "Authorization: Bearer $AGENTOS_API_TOKEN" http://127.0.0.1:8080/v1/state
curl -X POST -H "Authorization: Bearer $AGENTOS_API_TOKEN" http://127.0.0.1:8080/v1/control/pause
curl -X POST -H "Authorization: Bearer $AGENTOS_API_TOKEN" http://127.0.0.1:8080/v1/control/resume
curl -H "Authorization: Bearer $AGENTOS_API_TOKEN" http://127.0.0.1:8080/v1/artifacts/1
kill "$AGENTOS_PID"
wait "$AGENTOS_PID"
```

O artefato 1 existe após a confirmação do primeiro ciclo. A API aceita somente endereços loopback literais; token mínimo de 32 caracteres, sem CORS. Não é uma API multiusuário ou destinada à Internet. Ações: `pause`, `resume`, `cancel`. O serviço permanece disponível quando pausado ou concluído, até receber sinal de encerramento.

Pausa/cancelamento cancelam a atividade em andamento. Resultados tardios não são confirmados, inclusive se o operador retomar antes de o executor antigo sair. Isso não desfaz efeitos externos já realizados pelo script.

## Integridade e atualização de estado antigo

`init` registra SHA-256 do script principal e do executável Python, além de um fingerprint da versão, prefixos e inventário de nomes/versões de pacotes. Cada execução confere esse manifesto. O script executado corresponde aos bytes verificados; limite de 64 KiB no arquivo de entrada.

Para estados da v0.1, com serviço parado e sem ciclo pendente, revise os arquivos e execute `./agentos attest`. O comando aceita explicitamente a configuração atual. Não reatesta ciclos pendentes: restaure o script/ambiente original ou cancele a missão e crie outra.

Em `serve`, falha de execução pausa a missão e preserva o ciclo pendente. Em `run`, a falha encerra o processo com erro. Não há repetição automática ilimitada.

Os resultados ficam em `state/artifacts/<sha256>.json`. O snapshot referencia cada ciclo; leitura pela API verifica hash e tamanho. Arquivos órfãos após crash não são considerados resultados confirmados.

## Verificação

```bash
go test -race ./...
go vet ./...
go build -o agentos ./cmd/agentos
python3 -m unittest discover -s tests -v
```

Os testes cobrem replay, controle concorrente, descarte de resultado tardio, autenticação da API, artefatos corrompidos, mudança do script/ambiente, timeout, encerramento abrupto e restauração local.

## Limites explícitos

- Execute somente scripts confiáveis. `python3 -I` e subprocessos não restringem acesso ao filesystem ou à rede.
- O processo recebe ambiente reduzido, timeout de 30 segundos e saída limitada a 1 MiB. Não há limites de CPU/memória nem sandbox contra código hostil.
- A repetição é segura apenas para o exemplo de leitura. Escritas externas precisam de idempotência e reconciliação futuras.
- O snapshot contém a última memória, eventos e referências a resultados; não é uma trilha imutável ou assinada. O limite é 10.000 ciclos, até aproximadamente 10 GiB de resultados no pior caso. Não há quota total de disco.
- Manifestos detectam mudanças comuns, mas não verificam os bytes de todas as bibliotecas, módulos importados ou bibliotecas compartilhadas. Não protegem contra um operador hostil alterando simultaneamente código, manifesto e ambiente. Identidade criptográfica e autenticação multiusuário continuam pendentes.
- Não há serviço supervisor instalado: retomar automaticamente após reboot exige configuração operacional adicional.
- Persistência local atômica não substitui backup nem garante sobrevivência a perda de disco.

Este projeto é independente do agentOS da Rivet. Nenhum código daquele projeto foi incorporado.
