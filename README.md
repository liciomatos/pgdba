# pgdba-cli

Terminal UI para DBAs PostgreSQL — diagnóstico e gestão interativa direto no terminal.

## Instalação

### Binário pré-compilado (recomendado)

Baixe a versão mais recente em [Releases](https://github.com/liciomatos/pgdba/releases) para seu sistema operacional e arquitetura.

```bash
# Linux (amd64)
tar -xzf pgdba-cli_*_linux_amd64.tar.gz
chmod +x pgdba-cli
sudo mv pgdba-cli /usr/local/bin/
```

### Compilar do fonte

```bash
git clone https://github.com/liciomatos/pgdba.git
cd pgdba/pgdba-cli
go build -o pgdba-cli .
```

## Uso

```bash
# Via URI (recomendado)
pgdba-cli --url="postgres://user:password@host:5432/dbname?sslmode=disable"

# Via flags individuais
pgdba-cli --host=<host> --user=<user> --password=<password> --dbname=<dbname>
```

### Flags

| Flag | Env var | Padrão | Descrição |
|---|---|---|---|
| `--url` | `DATABASE_URL` | — | URI de conexão PostgreSQL (substitui todas as flags abaixo) |
| `--host` | `PGHOST` | — | Host do servidor |
| `--port` | `PGPORT` | `5432` | Porta |
| `--user` | `PGUSER` | `postgres` | Usuário |
| `--password` | `PGPASSWORD` | — | Senha |
| `--dbname` | `PGDATABASE` | `mydb` | Nome do banco |
| `--sslmode` | `PGSSLMODE` | `disable` | Modo SSL (`disable`, `require`, `verify-ca`, `verify-full`) |
| `--slow-ms` | `PG_SLOW_MS` | `1000` | Threshold em ms para considerar uma query lenta |

Todas as flags aceitam variáveis de ambiente como padrão. Se `--password` não for informado e `PGPASSWORD` estiver vazio, o arquivo `~/.pgpass` é consultado automaticamente.

### Exemplos

```bash
# Via variáveis de ambiente
export PGHOST=db.exemplo.com PGUSER=admin PGPASSWORD=senha
pgdba-cli --dbname=producao

# Threshold customizado para slow queries
pgdba-cli --url="postgres://admin:senha@db.exemplo.com/producao" --slow-ms=500
```

## Funcionalidades

Navegue com `↑↓` ou `j/k`. Pressione `r` para atualizar e `q`/`esc` para voltar.

No dashboard principal, acesse cada tela com a tecla de atalho indicada:

| Tecla | Tela | Descrição | Ações |
|---|---|---|---|
| `1` | **Slow Queries** | Top queries por tempo médio de execução¹ | — |
| `2` | **Long Running Queries** | Queries ativas há mais de 5 segundos | `k` matar sessão |
| `3` | **Replication Slots** | Slots e WAL acumulado | `d` dropar slot |
| `4` | **Blocked Queries** | Sessões bloqueadas e quem bloqueia | `t` terminar sessão, `a` terminar todas |
| `5` | **Connections** | Conexões por estado com % do limite | — |
| `6` | **Autovacuum** | Tabelas com mais dead tuples | `v` VACUUM ANALYZE |
| `7` | **Index Usage** | Índices por número de scans | `enter` detalhes do índice |
| `8` | **Cache Hit Ratio** | Taxa de acerto do buffer cache por tabela | — |
| `9` | **Users** | Usuários e permissões | — |
| `0` | **Roles** | Roles e membros | — |
| `p` | **Config** | Parâmetros do PostgreSQL (`pg_settings`) | — |
| `s` | **Schema Browser** | Tabelas e colunas por schema | `enter` descrever tabela |
| `e` | **Extensions** | Extensões instaladas | — |
| `D` | **Switch Database** | Trocar banco de dados sem reiniciar | `enter` conectar |
| `v` | **Version** | Versão do servidor PostgreSQL | — |

Todas as telas com lista suportam filtro em tempo real via `/`.

¹ Requer a extensão `pg_stat_statements`. Habilite com:
```sql
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
```

## Ambiente de desenvolvimento

```bash
# Subir PostgreSQL local via Docker
make docker-up

# Compilar e conectar no banco local
make run

# Rodar testes unitários (sem Docker)
cd pgdba-cli && go test ./... -short

# Rodar todos os testes (requer Docker)
cd pgdba-cli && go test ./... -timeout 120s
```

## Requisitos

- PostgreSQL 13 ou superior
- Para **Slow Queries**: extensão `pg_stat_statements` habilitada
