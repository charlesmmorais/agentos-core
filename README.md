# AgentOS Core

Micronúcleo em Go para agentes persistentes, com protocolo autônomo e executor Python separado.

**v0.4 — laboratório de continuidade, isolamento e análise com fontes, Linux/cgroup v2.** Executor Docker restrito, LLM opcional, memória com citações verificadas, API local autenticada e resultados por ciclo. Sem dependências Go externas. Não inclui RAG, MCP, HA ou DR automático.

## Visão

O agente é sua identidade, missão, protocolo, memória e compromissos persistidos. O processo é um executor substituível. Ele recebe a missão uma vez, executa ciclos agendados e retoma com a mesma identidade após reinício.

O núcleo controla estado, agendamento, limites e supervisão. Scripts realizam o trabalho. O exemplo Python observa arquivos locais sem modificá-los; sua saída JSON torna-se a memória do ciclo mais recente. Com cognição habilitada, o modelo interpreta a missão para produzir uma análise com fontes, sem escolher ferramentas ou alterar o protocolo. Leia [a configuração e os limites da cognição](docs/COGNITION.md).

Leia [a visão detalhada](docs/VISION.md), [a arquitetura](docs/ARCHITECTURE.md) e [a recuperação](docs/RECOVERY.md).

## Executar

Requisitos do modo isolado: Linux com cgroup v2, Go 1.23+, CLI e daemon Docker locais com limites de recursos e seccomp habilitados. O Python fica na imagem. Sem chaves de IA. O download inicial da imagem requer rede; a execução do script não tem rede.

```bash
git clone https://github.com/charlesmmorais/agentos-core.git
cd agentos-core
go build -o agentos ./cmd/agentos
docker pull python:3.12-slim
AGENTOS_IMAGE="$(docker image inspect --format '{{.Id}}' python:3.12-slim)"
./agentos init --image "$AGENTOS_IMAGE" --workspace examples/data --script examples/analyze.py --interval 5 --cycles 3 --mission "Acompanhar arquivos locais"
./agentos run
./agentos status
```

`init` registra caminhos absolutos e não sobrescreve estado existente. `run` encerra após o orçamento de ciclos. Para outra missão, use outro diretório: `--state state/outra-missao` em todos os comandos.

`docker` é o executor padrão para novas missões. É obrigatório informar o ID imutável `sha256:...` de uma imagem local confiável. Não existe fallback para Python do host. Para os exemplos antigos, exclusivamente com scripts confiáveis, use `init --executor trusted-host ...`. Estados existentes da v0.2 continuam no modo host até migração explícita por `attest --image "$AGENTOS_IMAGE"`, sem ciclo pendente.

Leia [isolamento e limitações](docs/SANDBOX.md) antes de habilitar scripts não confiáveis. Containers compartilham o kernel e não constituem uma VM ou uma garantia contra vulnerabilidades do runtime.

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

No modo host, `init` registra SHA-256 do script e executável Python e fingerprint do inventário de pacotes. No modo Docker, registra o script e ID imutável da imagem inteira. Cada execução confere seu manifesto. O script executado corresponde aos bytes verificados; limite de 64 KiB no arquivo de entrada.

Para atestar com serviço parado e sem ciclo pendente, revise os arquivos e execute `./agentos attest --image "$AGENTOS_IMAGE"` (isolado) ou `./agentos attest --executor trusted-host` (host). O comando aceita explicitamente a configuração atual. Não reatesta ciclos pendentes: restaure o script/ambiente original ou cancele a missão e crie outra.

Em `serve`, falha de execução pausa a missão e preserva o ciclo pendente. Em `run`, a falha encerra o processo com erro. Não há repetição automática ilimitada.

Os resultados ficam em `state/artifacts/<sha256>.json`. O snapshot referencia cada ciclo; leitura pela API verifica hash e tamanho. Arquivos órfãos após crash não são considerados resultados confirmados.

## Verificação

```bash
go test -race ./...
go vet ./...
go build -o agentos ./cmd/agentos
python3 -m unittest discover -s tests -v
```

Os testes cobrem replay, controle concorrente, descarte de resultado tardio, autenticação, artefatos corrompidos, integridade, timeout, encerramento abrupto e restauração local. Para executar os testes reais de isolamento, exporte `AGENTOS_SANDBOX_IMAGE` com o ID da imagem antes dos testes Python. Sem essa variável, os testes Docker são explicitamente ignorados; o job `sandbox` do CI sempre a fornece.

## Limites explícitos

- `trusted-host` exige scripts confiáveis e não limita filesystem/rede/CPU/memória. O modo Docker aplica isolamento e limites descritos em SANDBOX.md; não execute código hostil em produção sem avaliar o kernel, Docker e o modelo de ameaça.
- Docker: 128 MiB de memória, swap adicional zero, 0,5 CPU, 32 processos, /tmp de 16 MiB. Timeout padrão 30 segundos e saída de 1 MiB. Recursos efetivos são conferidos antes de executar o script.
- A repetição é segura apenas para o exemplo de leitura. Escritas externas precisam de idempotência e reconciliação futuras.
- O snapshot contém a última memória, eventos e referências a resultados; não é uma trilha imutável ou assinada. O limite é 10.000 ciclos, até aproximadamente 10 GiB de resultados no pior caso. Não há quota total de disco.
- No modo host, o manifesto não verifica todas as bibliotecas. No modo Docker, o ID cobre a imagem, mas não significa procedência confiável ou ausência de vulnerabilidades. O operador e o daemon são confiáveis; identidade criptográfica e autenticação multiusuário continuam pendentes.
- Não há serviço supervisor instalado: retomar automaticamente após reboot exige configuração operacional adicional.
- Persistência local atômica não substitui backup nem garante sobrevivência a perda de disco.

Este projeto é independente do agentOS da Rivet. Nenhum código daquele projeto foi incorporado.
