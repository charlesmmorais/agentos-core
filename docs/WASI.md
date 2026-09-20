# Fase 0.9 — executor WASI isolado e integrado ao protocolo

Missões Linux podem executar tarefas Go compiladas para WASI Preview 1. O contrato
é o mesmo do executor Python: entrada JSON do ciclo em stdin, objeto JSON de resultado
em stdout. O core mantém identidade, checkpoint, artefatos, orçamento, aprovações e
recuperação. O módulo não recebe ferramentas de escrita.

## Arquitetura

`SelectExecutor` seleciona `WASI`, que verifica o manifesto e inicia um processo
`agentos-wasi` por ciclo. O runtime wazero fica somente nesse executável auxiliar,
em `internal/wasiruntime`. `internal/core` usa apenas o contrato
`internal/wasiprotocol`; o binário principal não incorpora wazero.

A dependência é fixada em wazero v1.10.1, compatível com o piso Go 1.23 do projeto,
com checksums em go.sum. O worker usa o interpretador e não depende de CGO/JIT.
Atualizações do worker alteram seu hash e exigem reatestação explícita da missão.
A documentação da API está em [wazero v1.10.1](https://pkg.go.dev/github.com/tetratelabs/wazero@v1.10.1).

## Executar uma tarefa real

Em Linux, a partir da raiz do repositório:

```sh
mkdir -p build
go build -o build/agentos ./cmd/agentos
go build -o build/agentos-wasi ./cmd/agentos-wasi
GOOS=wasip1 GOARCH=wasm go build -o build/analyze.wasm ./examples/wasi-analyze
./build/agentos init --state state/wasi \
  --executor wasi --wasi-helper "$PWD/build/agentos-wasi" \
  --script "$PWD/build/analyze.wasm" --workspace examples/data \
  --mission 'dados dados sistemas' --cycles 1 --timeout 30
./build/agentos run --state state/wasi
./build/agentos status --state state/wasi
```

O resultado contém `word_count: 3`, `unique_words: 2` e frequências
`dados: 2`, `sistemas: 1`. O exemplo analisa o texto recebido na missão, não arquivos
do workspace. É possível escrever outras análises sobre `mission` e `memory`.
O parâmetro `--script` aponta para o módulo compilado neste perfil.

## Capacidades e limites da política v1

| Recurso | Regra |
| --- | --- |
| Módulo | Até 16 MiB; bytes SHA-256 conferidos antes do envio e no worker |
| Entrada do guest | Um objeto JSON de até 1 MiB, incluindo memória anterior e metadados |
| Saída do guest | Um objeto JSON de até 1 MiB; arrays, null e objetos concatenados recusados |
| stderr do guest | Até 64 KiB; não é incorporado ao resultado |
| Memória linear | Até 128 MiB; crescimento adicional não é concedido |
| Tempo | 1 a 300 segundos; padrão CLI de 30 segundos |
| Filesystem | Nenhum diretório pré-aberto; workspace não é montado |
| Rede | Nenhum socket, conexão ou extensão de rede concedido |
| Ambiente | Nenhuma variável herdada pelo worker ou repassada ao guest |
| Relógios | Wall clock, monotônico e sleep fornecidos para execução Go |
| Integrações | Apenas funções WASI Preview 1; nenhuma importação AgentOS/MCP/LLM/ação |

Excesso de stdout/stderr cancela o runtime, mesmo que o guest ignore o erro de escrita.
O core também limita a saída do processo. O contexto interrompe execução do guest e
o processo pai impõe timeout ao worker, incluindo compilação. Cada execução cria
uma instância nova, sem cache de memória do guest entre ciclos.

O `wasi_helper` é um executável nativo confiável indicado explicitamente pelo operador.
O manifesto fixa caminho absoluto, hash do helper, hash do módulo e política
`wasi-preview1-stdio-v1`. O caminho do helper deve permanecer protegido contra
substituição concorrente por outros usuários. Não há fallback para Docker ou host.

## Recuperação e efeitos externos

Antes de iniciar o worker, o core persiste o ciclo pendente. Interrupção preserva
seu identificador; a retomada pode executar novamente a computação e confirma um
único ciclo/artefato no estado. Isso não promete resultado bit a bit idêntico, pois
relógios existem, nem uma única invocação física. Em `serve`, falha ordinária pausa
a missão; em `run`, o erro encerra o processo e o checkpoint permanece retomável.

O guest não dispõe de canais para efeitos externos. Sua saída é dado, não comando.
Ações externas continuam sendo propostas, aprovadas e reconciliadas pelo mecanismo
existente de escrita; esta fase não adiciona `tools/call` ou aprovações implícitas.

Backups WASI usam `agentos.backup.v2`, com `module.wasm` em vez de `script.py`.
Backups Python continuam em v1, aceitos pelo leitor atual. O helper não é incluído:
restaure o mesmo executável no caminho atestado e confira o hash. Binários antigos
do AgentOS não leem o formato v2. O módulo é remapeado ao diretório restaurado,
enquanto identidade, ciclos e hashes são preservados. `recover --source-fenced`
e `resume` continuam obrigatórios para ativar uma missão restaurada.

## Limites da fronteira de isolamento

A barreira de acesso do guest é o runtime WebAssembly; o processo separado isola o
ciclo de vida e permite encerrar o worker. Não se trata de uma VM ou de isolamento
OS do helper contra uma vulnerabilidade do próprio runtime. O helper roda com o
usuário do AgentOS. O limite de 128 MiB cobre memória linear do guest, não memória
total do processo, tabelas, compilação ou consumo agregado de várias missões.
Produção com módulos hostis exige limites adicionais no supervisor/container e
atualização acompanhada do runtime. Há limite de tempo de parede, não quota de CPU.

## Validação e plataformas

A CI testa o worker em Linux, Windows e macOS: negação de leitura/escrita de arquivos,
conexão a um listener real, ausência de credenciais, limite de memória, loop infinito,
limites de saída, saída inválida, cancelamento e hash divergente. Em Linux, testa
ciclo interrompido, replay com ID preservado, artefato único, backup/restauração,
ativação assistida e alteração de módulo/helper.

`agentos capabilities` distingue `wasi_worker` de `durable_state`. O worker é
portátil; missões persistentes via CLI permanecem habilitadas apenas em Linux.
O frontend de navegador da fase 0.8 continua sendo um validador, sem hospedar esse
worker ou assumir a responsabilidade pelo estado durável.
