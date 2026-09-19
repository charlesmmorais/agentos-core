# AgentOS Core

Micronúcleo em Go para agentes persistentes, com protocolo autônomo e executor Python separado.

**v0.1 — laboratório de continuidade local, Linux/WSL2.** Implementação funcional sem dependências Go externas. Não inclui LLM, RAG, MCP, sandbox de segurança, HA ou DR automático nesta etapa.

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

O lock exclusivo protege todo o período de execução. Nesta versão, pare `run` antes de consultar ou alterar o estado por outra CLI:

```bash
./agentos pause
./agentos run    # não executa enquanto pausado
./agentos resume
./agentos run
```

`cancel` encerra administrativamente a missão; cancelamento e conclusão são terminais. Ctrl+C apenas encerra o executor e permite retomada posterior. Ainda não existe API de controle concorrente.

## Verificação

```bash
go test -race ./...
go vet ./...
go build -o agentos ./cmd/agentos
python3 -m unittest discover -s tests -v
```

Os testes cobrem replay com o mesmo identificador, pausa, cancelamento, orçamento, concorrência de escritores, estado inválido, integração Python, timeout, encerramento abrupto do processo e restauração local em outro diretório.

## Limites explícitos

- Execute somente scripts confiáveis. `python3 -I` e subprocessos não restringem acesso ao filesystem ou à rede.
- O processo recebe ambiente reduzido, timeout de 30 segundos e saída limitada a 1 MiB. Não há limites de CPU/memória nem sandbox contra código hostil.
- A repetição é segura apenas para o exemplo de leitura. Escritas externas precisam de idempotência e reconciliação futuras.
- O snapshot contém a última memória e eventos de ciclo; não é uma trilha imutável ou assinada. A v0.1 limita a missão a 10.000 ciclos.
- O protocolo e o script são administrados por um operador confiável. Não há verificação de hash do script na execução, identidade criptográfica ou autenticação multiusuário.
- Não há serviço supervisor instalado: retomar automaticamente após reboot exige configuração operacional adicional.
- Persistência local atômica não substitui backup nem garante sobrevivência a perda de disco.

Este projeto é independente do agentOS da Rivet. Nenhum código daquele projeto foi incorporado.
