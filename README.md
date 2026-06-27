# pgdba-cli

Terminal UI para DBAs PostgreSQL — diagnóstico e gestão interativa direto no terminal.

## Instalação

### Binário pré-compilado (recomendado)

Baixe a versão mais recente em [Releases](https://github.com/liciomatos/pgdba-cli/releases) para seu sistema operacional e arquitetura.

```bash
# Linux (amd64)
tar -xzf pgdba-cli_*_linux_amd64.tar.gz
chmod +x pgdba-cli
sudo mv pgdba-cli /usr/local/bin/
```

### Compilar do fonte

```bash
git clone https://github.com/liciomatos/pgdba-cli.git
cd pgdba-cli/pgdba-cli
go build -o pgdba-cli .
```

## Uso

```bash
pgdba-cli --host=<host> --user=<user> --password=<password> --dbname=<dbname>
```

### Flags

| Flag | Padrão | Descrição |
|---|---|---|
| `--host` | `$PGHOST` | Host do servidor PostgreSQL |
| `--port` | `$PGPORT` / `5432` | Porta |
| `--user` | `$PGUSER` / `postgres` | Usuário |
| `--password` | `$PGPASSWORD` | Senha |
| `--dbname` | `$PGDATABASE` / `mydb` | Nome do banco |
| `--sslmode` | `$PGSSLMODE` / `disable` | Modo SSL |

Todas as flags aceitam variáveis de ambiente como padrão. Se `--password` não for informado e `PGPASSWORD` estiver vazio, o arquivo `~/.pgpass` é consultado automaticamente.

### Exemplo com variáveis de ambiente

```bash
export PGHOST=db.exemplo.com
export PGUSER=admin
export PGPASSWORD=senha
pgdba-cli --dbname=producao
```

## Funcionalidades

Navegue com `↑↓` ou `j/k`. Pressione `Enter` para selecionar, `r` para atualizar e `q` para voltar.

| Tela | Descrição | Ações |
|---|---|---|
| **Check Version** | Versão do servidor PostgreSQL | — |
| **Slow Queries** | Top 10 queries por tempo médio de execução¹ | — |
| **Long Running Queries** | Queries ativas há mais de 5 segundos | `k` matar sessão |
| **Replication Slots** | Slots de replicação e WAL acumulado | `d` dropar slot |
| **Blocked Queries** | Sessões bloqueadas e quem está bloqueando | `t` terminar sessão, `a` terminar todas |
| **Connections Overview** | Conexões por estado com % do limite | — |
| **Autovacuum Monitor** | Tabelas com mais dead tuples | `v` VACUUM ANALYZE |
| **Index Usage** | Índices ordenados por número de scans | — |
| **Cache Hit Ratio** | Taxa de acerto do buffer cache por tabela | — |

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
