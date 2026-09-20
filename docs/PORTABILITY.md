# Fase 0.8 — núcleo portátil e capacidades por plataforma

**Atualização 0.9:** o [worker WASI separado](WASI.md) executa tarefas reais e é testado em Linux, Windows e macOS. Missões duráveis seguem restritas a Linux. As limitações de simulação abaixo descrevem os frontends da fase 0.8.

O objetivo desta entrega é tornar as regras do protocolo reutilizáveis em binários
nativos e WebAssembly, preservando as garantias operacionais do perfil Linux.
Compilação, execução de código portátil e operação durável são níveis diferentes.

## Matriz da entrega

| Ambiente | Compilação | Execução habilitada | Operação durável |
| --- | --- | --- | --- |
| Linux AMD64/ARM64 | CLI e simulador | Perfil existente, condicionado ao doctor e sandbox | Sim |
| macOS AMD64/ARM64 | CLI e simulador | Diagnóstico e validação local | Bloqueada |
| Windows AMD64 | CLI e simulador | Diagnóstico e validação local | Bloqueada |
| js/wasm | CLI de diagnóstico e frontend web | Validação em memória | Bloqueada |
| wasip1/wasm | CLI e simulador | Módulo Go de validação via host WASI | Bloqueada |

A CI executa o núcleo em runners Linux, Windows e macOS; faz compilação cruzada
para os sete alvos; executa módulos JS/WASM e WASI no Node. Isso não equivale a
homologação de browsers, todos os modelos de CPU ou recuperação nativa fora de Linux.

## Fronteiras

- `internal/kernel`: protocolo, eventos, decodificação estrita limitada a 64 KiB,
  regras de agendamento e transições pause/resume/cancel. Sem filesystem,
  subprocessos, rede ou leitura implícita do relógio.
- `internal/platform`: capacidades implementadas por perfil. Uma capacidade
  desconhecida é recusada. A presença de uma capacidade não atesta Docker,
  configuração, permissões, disponibilidade de disco ou serviços externos.
- `internal/core`: orquestração durável, aprovação, ações, recuperação e adaptadores.
  As primitivas Unix estão isoladas por build tags; os demais alvos falham de modo
  explícito, sem substituir locks ou sincronização por operações mais fracas.
- `cmd/agentos-sim`: interface stdin/stdout do núcleo, compilável para nativo e WASI.
- `cmd/agentos-web`: ponte JS para o mesmo núcleo, com entrada JSON e saída JSON.

A extração é incremental: estado durável, orçamento do modelo, aprovação e RAG
continuam no core. Não há um segundo agente autônomo no navegador. A validação
portátil aceita sintaxe de caminhos Unix e Windows; o host Linux continua exigindo
caminhos absolutos válidos para o seu próprio filesystem antes de criar/carregar missões.

## Diagnóstico e validação local

```sh
go build -o agentos ./cmd/agentos
./agentos capabilities
go run ./cmd/agentos-sim < examples/protocol-portable.json
```

No Windows, pode-se compilar com `-o agentos.exe` e executar `./agentos.exe capabilities`.
Os comandos operacionais recusam um perfil sem `durable_state` antes de criar estado.
A saída `capabilities` é inventário estático, não um teste de saúde.

A simulação desta fase verifica configuração e orçamento de ciclos. Não executa o
script informado, não gera resultados de análise, não aprova ações e não grava estado.

## Navegador

Com Go 1.24, em shell POSIX:

```sh
GOOS=js GOARCH=wasm go build -o examples/web/agentos.wasm ./cmd/agentos-web
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/web/
python3 -m http.server 8000 --bind 127.0.0.1 --directory examples/web
```

Abra `http://127.0.0.1:8000`. Em Go 1.23, o arquivo de suporte está em `misc/wasm`.
Use o suporte JavaScript da mesma versão do compilador. A página roda a validação
localmente; não comunica com a API do serviço e não requer credenciais.
Binários e o suporte gerado não são versionados.

## WASI

```sh
GOOS=wasip1 GOARCH=wasm go build -o /tmp/agentos-sim.wasm ./cmd/agentos-sim
node tests/wasm_smoke.cjs wasi /tmp/agentos-sim.wasm < examples/protocol-portable.json
```

O harness usa Node 22 e WASI Preview 1, sem diretórios pré-abertos ou variáveis de
ambiente repassadas ao módulo. Ele é destinado somente ao módulo confiável gerado
pelo próprio projeto. **Não é um executor seguro de módulos arbitrários**: Node WASI
não deve ser usado como fronteira de segurança para código não confiável. O executor de missões da fase 0.9 usa um worker wazero separado, descrito em [WASI.md](WASI.md); este harness Node continua restrito ao validador confiável.

## Garantias preservadas e próximos adaptadores

O formato de estado permanece versão 1. Locks, fsync, publicação de backup,
aprovação vinculada a digest e reconciliação de ações não foram substituídos.
A suíte Linux e os jobs Docker/systemd continuam obrigatórios.

Para habilitar operação durável no Windows ou macOS, uma entrega posterior precisa
implementar e testar exclusão mútua, publicação atômica, sincronização, controle da
árvore de processos, comunicação com o executor e supervisor nativo. Suporte a
Docker Desktop não pode ser inferido da compilação: os mounts e sockets atravessam
outra fronteira de host. A recuperação deve ser validada com falhas reais antes de
habilitar a capacidade. O executor WASI separado está disponível desde a fase 0.9.
