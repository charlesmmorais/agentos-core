# Recuperação e DR — fase 0.7

O AgentOS oferece backup consistente do estado local, restauração assistida e configuração de supervisão externa por systemd. O backup exige a parada do proprietário do estado; a restauração cria outro diretório e não inicia a missão automaticamente. Não há HA, eleição de líder ou reconstrução automática de infraestrutura.

## Criar e verificar um backup

Pare `run`/`serve` antes do backup. O lock exclusivo impede exportar enquanto outro processo AgentOS usa o estado. Em implantação systemd, o job descrito adiante faz a parada e a retomada operacional.

```bash
./agentos backup --state state/minha-missao \
  --output /caminho-seguro/agentos-backup.tar \
  > /caminho-seguro/agentos-backup.receipt.json
./agentos backup-verify --archive /caminho-seguro/agentos-backup.tar \
  --sha256 SHA256_DO_RECIBO
```

O diretório de saída deve existir. `backup` não substitui arquivos existentes e publica o TAR completo somente depois de sincronizar seu conteúdo. O recibo JSON informa SHA-256 do arquivo, identidade, estado original, ciclos, quantidade de arquivos, bytes e data. Guarde esse recibo por um canal confiável, preferencialmente separado da cópia.

O TAR, sem compressão, contém:

- `state.json`: identidade, protocolo, agenda, memória, orçamentos, aprovações, intenções, recibos e eventos.
- `artifacts/<sha256>.json`: somente artefatos referenciados pelo checkpoint, com hashes e tamanhos conferidos.
- `script.py`: bytes do script atestado, conferidos contra seu manifesto.
- `workspace/`: cópia limitada das entradas locais, pelas regras de exportação do executor.
- `manifest.json`: inventário de caminhos, tamanhos e SHA-256 de cada arquivo.

Não inclui locks, temporários, artefatos órfãos, tokens do ambiente, binário Go, executável/pacotes Python, imagens Docker ou dados de sistemas externos. Dados sensíveis presentes no workspace, memória e payloads continuam no arquivo; não há criptografia ou assinatura de backup. Proteja acesso, armazenamento e transporte.

Consistência significa um checkpoint local sob lock e todos os artefatos que ele referencia. Workspace e script são capturados depois, sem transação com produtores externos; suspenda esses produtores se precisar de uma versão conjunta. Recursos MCP, modelos e recibos remotos não formam um snapshot distribuído.

Limites: até 256 MiB para payload e inventário, 32 MiB para estado, 1 MiB por artefato, 64 KiB para script e 4 MiB por arquivo de workspace, com 16 MiB/1.000 arquivos no workspace. O leitor limita o TAR a 512 MiB e 12.004 entradas. Históricos maiores exigem evolução do formato ou estratégia operacional de retenção; não são truncados silenciosamente.

`backup-verify` confere inventário e hashes internos mesmo sem `--sha256`; quando informado, confere também o hash externo. Rejeita caminhos fora do formato, travessia de diretórios, links, arquivos especiais, entradas duplicadas, dados extras após o TAR e artefatos ausentes/corrompidos. Usa staging temporário e precisa de espaço livre.

## Restaurar em outro diretório ou host

1. Prepare um host Linux, binário AgentOS e runtime original. Para Docker, recupere a imagem confiável com o mesmo ID; para `trusted-host`, reconstrua Python e o inventário atestado.
2. Desative ou isole a instância original. O flock é local e não impede outra máquina de executar a mesma identidade.
3. Traga o backup e confira o SHA-256 registrado por canal confiável.
4. Restaure em um caminho inexistente:

```bash
./agentos restore --state /var/lib/agentos/restaurado \
  --archive /caminho-seguro/agentos-backup.tar --sha256 SHA256_DO_RECIBO
./agentos status --state /var/lib/agentos/restaurado
./agentos doctor --state /var/lib/agentos/restaurado
```

O hash externo é obrigatório no `restore`. O comando valida o pacote em staging e recusa destino existente. Mantém identidade, memória, contadores, orçamento, ciclos pendentes e hashes de aprovação. Remapeia os caminhos para `script.py` e `workspace/` dentro do destino; não reatesta automaticamente o runtime.

A publicação grava dependências primeiro, sob lock exclusivo, e publica `state.json` por último como marcador de conclusão, com fsync. Uma falha anterior pode deixar diretório incompleto, sem estado carregável. Ele não é removido automaticamente: inspecione antes de descartar e repita em outro caminho. O estado original e o backup permanecem preservados.

Missões ativas tornam-se pausadas; pausadas continuam pausadas; canceladas/concluídas preservam o estado terminal. O registro `recovery` mantém o hash de origem e bloqueia retomada/escrita até a validação assistida. `doctor` verifica artefatos, script, manifesto do runtime e regras de exportação do workspace. Não testa credenciais, disponibilidade de provedores, saúde do host ou isolamento da origem.

Depois de conferir ambiente, dependências, credenciais e desativação da origem:

```bash
./agentos recover --state /var/lib/agentos/restaurado --source-fenced
./agentos resume --state /var/lib/agentos/restaurado
./agentos run --state /var/lib/agentos/restaurado
```

`--source-fenced` é declaração administrativa explícita, não verificação remota. `recover` repete a validação local e libera a proteção; não muda o status para ativo. `resume` permanece recusado para missões canceladas/concluídas. Divergência de script/runtime bloqueia a liberação. Prefira reconstruir o ambiente original; `attest` continua uma aceitação manual distinta, proibida enquanto houver ciclo pendente.

O script principal é relocável. Caminhos adicionais embutidos no código, diretórios não exportados e dependências `trusted-host` precisam de recuperação própria. Docker usa os caminhos internos padronizados do executor, mas a imagem não está no TAR.

## Escritas e backups antigos

Uma ação aprovada no backup pode ter sido executada depois dele. Ações `approved`, `in_flight`, `unknown` ou `retry_required` são restauradas como `unknown`, mantendo ID, digest e aprovação. Nenhuma ganha permissão de reenviar PUT na restauração.

É permitido consultar o destino antes da liberação do ambiente:

```bash
./agentos action-reconcile --state /var/lib/agentos/restaurado --intent ID
```

Se o recibo existir, ele é registrado sem novo PUT. Se o destino informar ausência, será necessária a sequência explícita de retry em [WRITES.md](WRITES.md), após liberação da recuperação. Ausência momentânea não prova que uma requisição antiga nunca produzirá efeito; ID estável e idempotência do destino continuam obrigatórios.

Propostas não aprovadas permanecem propostas. Recibos confirmados permanecem confirmados; perda posterior de dados do destino exige investigação externa. O backup também restaura contadores antigos, não os consumos posteriores: limites globais devem existir no provedor/destino. O AgentOS não reconstrói o ledger de idempotência de outro sistema.

## Supervisor externo e agenda

| Arquivo em deploy/ | Responsabilidade |
|---|---|
| `agentos@.service` | Executar serve e reiniciar após falha, respeitando a política persistida |
| `agentos-backup@.service` | Parar, exportar/verificar e retomar a instância se estava ativa |
| `agentos-backup@.timer` | Backup horário, atraso aleatório de até 60 segundos e execução pendente após indisponibilidade |
| `backup.sh` | Aplicar parada, exportação e verificação; retomar o serviço após falhas ordinárias de backup |

O systemd roda fora do processo AgentOS. A configuração usa `Restart=on-failure`, espera de 5 segundos e limite de três partidas em 60 segundos, conforme os mecanismos de [reinício e limitação do systemd](https://github.com/systemd/systemd/blob/main/man/systemd.service.xml). Uma parada administrativa não pede reinício automático. Não há watchdog: término do processo é detectado, mas nem todo travamento.

O serviço usa usuário dedicado `agentos`, estado em `/var/lib/agentos/INSTANCIA`, binário em `/opt/agentos/agentos` e ambiente em `/etc/agentos/INSTANCIA.env`. O script aceita nomes de instância com letras, números, `_` e `-`. Cada serviço precisa de porta própria. Exemplo do arquivo de ambiente, pertencente a root e protegido com `0600`:

```ini
AGENTOS_LISTEN=127.0.0.1:8080
AGENTOS_API_TOKEN=TOKEN_ALEATORIO_COM_PELO_MENOS_32_CARACTERES
```

Inclua credenciais opcionais LLM/MCP/escrita somente quando necessárias. O template define `PATH=/usr/bin:/bin`: missões `trusted-host` devem ser atestadas nesse mesmo ambiente. O estado deve pertencer a `agentos`; scripts/dados precisam ser legíveis. Conceder acesso ao daemon Docker ao usuário é uma decisão privilegiada, equivalente a confiar-lhe controle do host. Não use `PrivateTmp=yes`: o daemon precisa enxergar os caminhos de staging montados pelo cliente.

Instalação no host escolhido, após criar usuário, preparar estado/runtime e configurar o ambiente:

```bash
sudo install -d /opt/agentos/deploy /etc/agentos /var/lib/agentos /var/backups/agentos
sudo install -m 755 agentos /opt/agentos/agentos
sudo install -m 644 deploy/backup.sh /opt/agentos/deploy/backup.sh
sudo install -m 644 deploy/agentos@.service deploy/agentos-backup@.service deploy/agentos-backup@.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now agentos@INSTANCIA.service
sudo systemctl enable --now agentos-backup@INSTANCIA.timer
```

O repositório fornece e testa a configuração; não instala serviços no seu host automaticamente. O timer implica breve indisponibilidade planejada: o backup é offline. Reiniciar `serve` mantém missões pausadas, canceladas ou concluídas sem reabrir a execução.

Backups ficam em `/var/backups/agentos/INSTANCIA`. Para outro volume, configure `AGENTOS_BACKUP_ROOT=/montagem/backup` em `/etc/agentos/INSTANCIA.backup.env` e declare a dependência de montagem no unit, como `RequiresMountsFor=/montagem/backup`. Verifique que o destino está realmente montado antes de habilitar o timer.

Não há exclusão automática, quota, rotação, upload para nuvem ou alertas. Monitore espaço, recibos e falhas pelo journal. Interromper o job ou matar o host pode deixar a missão parada; inspecione e retome administrativamente. Para manutenção, pare/desabilite o timer e o job antes de parar o serviço principal, evitando a retomada programada ao fim de um backup em andamento.

## Alcance e validação

Uma cópia no mesmo disco não cobre perda física do host. Configure destino externo, retenção, criptografia e proteção contra exclusão; preserve binário, imagem/runtime, credenciais e dependências remotas. A agenda não garante RPO. RPO depende do último backup externo válido; RTO deve incluir reconstrução de runtime, transferência, verificação, reconciliação e liberação administrativa.

O CI valida corrupção/caminhos maliciosos; retomada após remoção de script/workspace; identidade e ciclos; bloqueio de ativação/script adulterado; reconciliação de efeito posterior ao backup; fluxo Docker; reinício real pelo systemd após SIGKILL mantendo missão pausada; e job de backup com parada/verificação/retomada. Não é teste regional, de provedor de nuvem ou de restauração de imagem Docker em host vazio.
