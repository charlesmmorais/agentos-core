# Isolamento v0.3

## Fronteira

O núcleo Go chama o Docker do host. O convidado recebe uma cópia limitada dos arquivos de entrada, o script atestado, um SDK e um socket Unix de capacidades restritas. Nunca recebe o socket Docker, diretório de estado, token da API ou credenciais do host.

Containers usam o kernel do host: esta fase não implementa microVM, gVisor, proteção contra falhas de kernel ou prova formal. Daemon Docker, imagem, configuração e operador são confiáveis. Recomendamos host de testes dedicado. Exposição multi-tenant hostil não está homologada.

## Controles aplicados

| Recurso | Restrição |
|---|---|
| Rede | `--network=none`; sem rede externa ou rede loopback do host |
| Identidade | UID/GID 65534, sem capabilities, no-new-privileges |
| Syscalls | Seccomp ativo requerido; perfil padrão do Docker |
| Raiz | Read-only |
| Workspace | Exportação somente leitura, primeiro nível, sem links/sockets/diretórios/arquivos ocultos |
| Exportação | Até 1000 entradas, 4 MiB por arquivo e 16 MiB total |
| Temporários | /tmp tmpfs de 16 MiB, noexec/nosuid/nodev |
| Memória | 128 MiB, swap adicional zero |
| CPU | Quota 0,5 CPU |
| Processos | pids.max 32 |
| Saída | stdout/stderr até 1 MiB cada; log Docker desabilitado |
| Tempo | Timeout do protocolo; limpeza do container após execução |

O bootstrap verifica memory.max, memory.swap.max, cpu.max, pids.max, Seccomp, NoNewPrivs e CapEff antes de executar código convidado. Sem cgroup v2 ou limites efetivos, falha fechado. A verificação do estado Seccomp confirma filtro ativo, não audita filtros personalizados: não configurar perfis permissivos no daemon.

## Broker e SDK

O script pode `import agentos` e chamar:

```python
identity = agentos.context()
previous = agentos.memory()
```

O broker Go aceita somente `context.get` e `memory.read`, com dados da execução atual. Não aceita caminhos, comandos, escrita ou chamadas de rede. Métodos não autorizados recebem `capability_denied`.

Protocolo JSON por linha sobre socket Unix, uma requisição por conexão, até 4 conexões simultâneas, entrada até 4 KiB e timeout de 2 segundos. O socket termina junto com a execução. O token HTTP não participa desse protocolo. Não há invocação MCP nesta fase.

Persistência continua mediada pelo núcleo: saída JSON validada vira artefato e memória após a checagem de estado/geração. Cancelamento impede confirmação tardia.

## Imagens e ambiente

Somente ID local `sha256:<64 hex>`; tags são rejeitadas. `--pull=never` impede mudança de imagem durante a execução. O operador baixa e revisa a imagem antes de atestar. O exemplo usa Python slim; bibliotecas adicionais devem estar em uma imagem previamente construída, nunca instaladas dinamicamente pelo script.

É necessário Docker local: mounts e socket Unix pressupõem o mesmo host. Docker remoto não é suportado. Rootless depende de suporte real aos limites cgroup e permissões de mounts; não é homologado nesta fase.

## Falhas e limites

- Falta de Docker, imagem, manifesto ou limites não inicia o Python do host.
- Timeout/cancelamento normal remove apenas o container identificado e rotulado desta missão.
- SIGKILL do núcleo pode deixar um container órfão; a próxima execução da mesma identidade remove somente esse container se o rótulo corresponder. Não há supervisor externo nem limpeza após perda definitiva do host.
- Uma falha de comunicação com o daemon pode impedir limpeza imediata. O operador deve verificar containers; não se promete término remoto garantido.
- Não execute duas cópias da mesma identidade em diretórios restaurados simultaneamente. O lock é local, não fencing distribuído.
- A cópia de entrada evita expor sockets e links. A escolha de arquivos continua responsabilidade do operador: não usar diretório com dados sensíveis não destinados à missão.
- O broker só lê snapshots; quota de requisições por missão e logs detalhados de capacidades são próximos aprimoramentos.

## Validação

O job CI `sandbox` baixa uma imagem Python, fixa seu ID e executa testes reais de leitura/escrita, bloqueio de rede, identidade, broker, limites efetivos, memória excedida, timeout e limpeza. Testes de construção de argumentos não substituem esse job.
