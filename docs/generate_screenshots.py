#!/usr/bin/env python3
"""Generate SVG screenshots for pgdba-cli docs."""

import os
from rich.console import Console
from rich.table import Table
from rich.text import Text
from rich import box

SCREENSHOTS_DIR = os.path.join(os.path.dirname(__file__), "screenshots")

# Match pgdba TUI colors
C_BLUE   = "color(39)"
C_GRAY   = "color(240)"
C_WHITE  = "color(255)"
C_GREEN  = "color(10)"
C_YELLOW = "color(11)"
C_RED    = "color(9)"
C_DARK   = "color(244)"


def make_console(width=120):
    return Console(record=True, width=width, force_terminal=True,
                   color_system="256", highlight=False)


def header(screen_name, conn="postgres@localhost:5432/mydb"):
    return (f"[bold {C_BLUE}]pgdba[/] [{C_GRAY}] › [/] [bold {C_WHITE}]{screen_name}[/]\n"
            f"[dim]{conn}[/]\n")


def footer(hints):
    return f"[dim]/ filter • {hints}[/]"


def key(k):
    return f"[bold {C_BLUE}]{k}[/]"


def label(t):
    return f"[{C_GRAY}]{t}[/]"


def ok(t):
    return f"[{C_GREEN}]{t}[/]"


def warn(t):
    return f"[{C_YELLOW}]{t}[/]"


def crit(t):
    return f"[{C_RED}]{t}[/]"


def dim(t):
    return f"[dim]{t}[/]"


def val(t):
    return f"[{C_WHITE}]{t}[/]"


def save(name, console):
    svg = console.export_svg(title="pgdba-cli")
    path = os.path.join(SCREENSHOTS_DIR, f"{name}.svg")
    with open(path, "w") as fh:
        fh.write(svg)
    print(f"  {name}.svg")


def make_table(columns, rows, col_styles=None):
    """Build a rich Table matching pgdba TUI DefaultTableStyles."""
    t = Table(box=box.SIMPLE_HEAD, show_header=True, header_style=f"bold {C_BLUE}",
              border_style=C_GRAY, pad_edge=False, show_edge=False)
    for col in columns:
        t.add_column(col[0], width=col[1], no_wrap=True)
    for row in rows:
        styled = []
        for i, cell in enumerate(row):
            if col_styles and i < len(col_styles) and col_styles[i]:
                styled.append(col_styles[i](cell))
            else:
                styled.append(cell)
        t.add_row(*styled)
    return t


# ── Dashboard ──────────────────────────────────────────────────────────────────

def gen_dashboard():
    c = make_console()
    c.print(header("Dashboard", "postgres@localhost:5432/mydb  (v15.3)"))

    lw = 28
    label_s  = lambda t: f"[{C_GRAY}]{t:<{lw}}[/]"
    metric   = lambda lbl, v, lvl=0: c.print(
        f"  {label_s(lbl)}  {[ok, warn, crit][lvl](v)}"
    )

    metric("Connections",           "87 / 200  (44%)")
    metric("Active queries",        "3")
    metric("Blocked queries",       "0")
    metric("Slow queries (>1000ms)","12",          1)
    metric("Cache hit ratio",       "98.7%")
    metric("Dead tuples",           "4,230")
    metric("Invalid indexes",       "0")
    metric("Replication slots",     "2")
    metric("Freeze status",         "oldest: mydb  189M txns (9.0%)")

    c.print()
    divider = f"[{C_GRAY}]{'─' * 65}[/]"
    c.print(divider)
    row1 = (f"{key('1')} {label('slow')}  {key('2')} {label('longrun')}  "
            f"{key('3')} {label('slots')}  {key('4')} {label('locks')}  "
            f"{key('5')} {label('conn')}  {key('6')} {label('autovac')}  "
            f"{key('7')} {label('index')}  {key('8')} {label('cache')}")
    row2 = (f"{key('9')} {label('users')}  {key('0')} {label('roles')}  "
            f"{key('p')} {label('config')}  {key('s')} {label('schema')}  "
            f"{key('e')} {label('ext')}  {key('D')} {label('switch-db')}  "
            f"{key('L')} {label('load')}  {key('w')} {label('waits')}  "
            f"{key('f')} {label('freeze')}")
    row3 = (f"{key('S')} {label('db-size')}  {key('t')} {label('temp-files')}  "
            f"{key('m')} {label('memory')}  {key('r')} {label('refresh')}  "
            f"{key('q')} {label('quit')}")
    c.print(row1)
    c.print(row2)
    c.print(row3)
    save("dashboard", c)


# ── Autovacuum (simplified columns) ────────────────────────────────────────────

def gen_autovacuum():
    c = make_console()
    c.print(header("Autovacuum Monitor"))

    cols = [("Schema", 12), ("Table", 25), ("Dead Tuples", 12),
            ("Dead %", 8), ("Size", 10), ("Last Autovacuum", 18), ("Autovac Count", 14)]

    def dead_pct_style(v):
        try:
            f = float(v.rstrip("%"))
            if f > 30: return crit(v)
            if f > 10: return warn(v)
        except ValueError:
            pass
        return v

    rows = [
        ("public", "orders",       "82,000", "6.2%",  "4.2 GB",  "2026-06-29 11:45", "8,831"),
        ("public", "events",       "38,100", "3.8%",  "8.1 GB",  "2026-06-29 10:12", "3,421"),
        ("public", "sessions",     "21,500", "38.4%", "640 MB",  "2026-06-28 22:05", "1,203"),
        ("audit",  "audit_log",    "14,500", "1.2%",  "2.3 GB",  "2026-06-28 03:12", "141"),
        ("public", "products",      "9,800", "12.3%", "320 MB",  "2026-06-27 14:33", "892"),
        ("public", "users",         "3,200", "0.8%",  "128 MB",  "2026-06-29 08:01", "4,102"),
        ("public", "inventory",       "840", "N/A",   "48 MB",   "never",            "0"),
    ]

    col_styles = [None, None, None, dead_pct_style, None, None, None]
    c.print(make_table(cols, rows, col_styles))
    c.print(footer("enter detail • v vacuum analyze • r refresh • q back"))
    save("autovacuum", c)


# ── Autovacuum Detail ───────────────────────────────────────────────────────────

def gen_autovacuum_detail():
    c = make_console()
    c.print(header("Autovacuum Detail — public.orders"))

    section = lambda t: f"[bold {C_BLUE}]{t}[/]"
    lbl     = lambda t: f"[{C_GRAY}]{t:<26}[/]"

    c.print(section("STATS"))
    c.print(f"  {lbl('Live tuples:')} {val('1,240,000')}     {lbl('Table size:')} {val('3.8 GB')}")
    c.print(f"  {lbl('Dead tuples:')} {val('82,000')}        {lbl('Total size:')} {val('4.2 GB')}")
    c.print(f"  {lbl('Modified since analyze:')} {val('18,500')}   {lbl('Toast+Index:')} {val('400 MB')}")
    c.print()

    c.print(section("VACUUM HISTORY"))
    c.print(f"  {lbl('Last vacuum:')}     {val('2026-06-29 03:12:07')}     Count: {val('142')}")
    c.print(f"  {lbl('Last autovacuum:')} {val('2026-06-29 11:45:31')}     Count: {val('8,831')}")
    c.print(f"  {lbl('Last analyze:')}    {val('2026-06-28 03:12:05')}     Count: {val('141')}")
    c.print(f"  {lbl('Last autoanalyze:')}{val('2026-06-29 11:45:31')}     Count: {val('8,830')}")
    c.print()

    c.print(section("FREEZE STATUS"))
    bar = "█" * 4 + "░" * 16
    c.print(f"  {lbl('relfrozenxid age:')} {ok('42,300,000')}   [{C_DARK}]{bar}[/]  21.2%")
    c.print(f"  {lbl('relminmxid age:')}   {val('3,100,000')}")
    c.print()

    c.print(section("CUSTOM PARAMETERS  (* = overridden for this table)"))
    c.print(f"  [{C_GRAY}]{'Parameter':<42} {'Table value':<16} Global value[/]")
    params = [
        ("autovacuum_analyze_scale_factor", "",      "0.1"),
        ("autovacuum_analyze_threshold",    "",      "50"),
        ("autovacuum_freeze_max_age",        "",     "200000000"),
        ("autovacuum_freeze_min_age",        "",     "50000000"),
        ("autovacuum_freeze_table_age",      "",     "150000000"),
        ("autovacuum_vacuum_cost_delay",     "",     "2"),
        ("autovacuum_vacuum_cost_limit",     "",     "-1"),
        ("autovacuum_vacuum_scale_factor",  "0.01 *","0.2"),
        ("autovacuum_vacuum_threshold",      "",     "50"),
    ]
    for name, tval, gval in params:
        tv_str = f"[{C_YELLOW}]{tval}[/]" if tval else f"[dim](inherited)[/]"
        c.print(f"  [{C_GRAY}]{name:<42}[/] {tv_str:<16} {val(gval)}")
    c.print()

    c.print(section("PRECISE BLOAT  (pgstattuple)"))
    c.print(f"  [dim]b → run pgstattuple for precise bloat (full table scan)[/]")

    c.print()
    c.print(f"[dim]↑↓ scroll • b precise bloat • v vacuum analyze • r refresh • q back[/]")
    save("autovacuum_detail", c)


# ── Freeze Monitor ─────────────────────────────────────────────────────────────

def gen_freeze():
    c = make_console()
    c.print(header("Freeze Monitor"))

    c.print(f"[bold {C_BLUE}]DATABASE XID STATUS[/]")
    c.print(f"  [{C_GRAY}]{'Database':<22}  {'XID Age':<16}  {'% Shutdown':<12}  Status[/]")
    dbs = [
        ("mydb",       189_400_000, 9.02, 1),
        ("analytics",   98_100_000, 4.67, 0),
        ("staging",     41_200_000, 1.96, 0),
        ("template1",    3_500_000, 0.17, 0),
    ]
    for db, age, pct, lvl in dbs:
        status = [ok("OK"), warn("Warning"), crit("Critical")][lvl]
        pct_s  = [ok, warn, crit][lvl](f"{pct:.2f}%")
        c.print(f"  {db:<22}  {age:<16,}  {pct_s:<12}  {status}")
    c.print()

    c.print(f"[bold {C_BLUE}]TOP TABLES BY XID AGE[/]")
    cols = [("Schema", 12), ("Table", 25), ("XID Age", 12), ("MXI Age", 12),
            ("% Freeze", 10), ("Size", 10), ("Last Autovacuum", 18)]

    def pct_style(v):
        try:
            f = float(v.rstrip("%"))
            if f > 75: return crit(v)
            if f > 50: return warn(v)
            return ok(v)
        except ValueError:
            return v

    rows = [
        ("public", "orders",    "42,300,000", "310,000", "21.2%", "4.2 GB", "2026-06-29 11:45"),
        ("public", "events",    "38,100,000", "120,000", "19.1%", "8.1 GB", "2026-06-29 10:12"),
        ("audit",  "audit_log", "31,500,000",  "80,000", "15.8%", "2.3 GB", "2026-06-28 03:12"),
        ("public", "sessions",  "28,900,000", "210,000", "14.5%", "640 MB", "2026-06-28 22:05"),
        ("public", "products",  "22,100,000",  "42,000", "11.1%", "320 MB", "2026-06-27 14:33"),
    ]
    col_styles = [None, None, None, None, pct_style, None, None]
    c.print(make_table(cols, rows, col_styles))
    c.print(footer("f vacuum freeze selected table • r refresh • q back"))
    save("freeze", c)


# ── Replication Slots (enriched) ───────────────────────────────────────────────

def gen_replication_slots():
    c = make_console()
    c.print(header("Replication Slots"))

    cols = [("Slot Name", 22), ("Plugin", 14), ("Type", 10),
            ("Active", 8), ("WAL Lag", 12), ("Safe WAL", 12)]

    def active_style(v):
        return ok(v) if v == "true" else crit(v)

    rows = [
        ("pgdba_replica",       "",          "physical", "true",  "256 bytes", "1.0 GB"),
        ("test_logical_slot",   "pgoutput",  "logical",  "true",  "48 bytes",  "1.0 GB"),
        ("test_physical_slot",  "",          "physical", "true",  "512 bytes", "1.0 GB"),
        ("stale_logical_slot",  "wal2json",  "logical",  "false", "2.1 GB",    "512 MB"),
    ]
    col_styles = [None, None, None, active_style, None, None]
    c.print(make_table(cols, rows, col_styles))
    c.print(footer("d drop • s standbys • p config • r refresh • q back"))
    save("replication_slots", c)


# ── Streaming Standbys ─────────────────────────────────────────────────────────

def gen_replication_standbys():
    c = make_console()
    c.print(header("Streaming Replication — Standbys"))

    cols = [("App Name", 18), ("Client", 16), ("State", 12), ("Sync", 8),
            ("Write Lag", 20), ("Flush Lag", 20), ("Replay Lag", 20), ("Lag Bytes", 12)]

    def sync_style(v):
        if v in ("sync", "quorum"): return ok(v)
        if v == "async":            return dim(v)
        return v

    rows = [
        ("pgdba_replica", "172.18.0.3", "streaming", "async",
         "00:00:00.012", "00:00:00.015", "00:00:00.022", "1,024"),
        ("analytics_ro",  "172.18.0.4", "streaming", "sync",
         "00:00:00.001", "00:00:00.002", "00:00:00.003", "0"),
        ("delayed_dr",    "10.0.2.5",   "streaming", "async",
         "02:30:14.000", "02:30:14.001", "02:30:14.002", "1,073,741,824"),
    ]
    col_styles = [None, None, None, sync_style, None, None, None, None]
    c.print(make_table(cols, rows, col_styles))
    c.print(footer("k terminate walsender • r refresh • q back"))
    save("replication_standbys", c)


# ── Replication Config ─────────────────────────────────────────────────────────

def gen_replication_config():
    c = make_console()
    c.print(header("Replication Config"))

    c.print(f"  [{C_GRAY}]Active senders:[/] {val('1')}   [{C_GRAY}]Total slots:[/] {val('4')}   [{C_GRAY}]Active slots:[/] {val('3')}")
    c.print()

    cols = [("Parameter", 35), ("Setting", 18), ("Unit", 6), ("Context", 12), ("Hint", 45)]

    def hint_style(v):
        if any(w in v for w in ("risk", "disabled", "corruption", "loss")):
            return crit(v)
        if any(w in v for w in ("required", "may cause", "will be", "not available")):
            return warn(v)
        if any(w in v for w in ("supports", "OK", "streaming")):
            return ok(v)
        return dim(v) if v else v

    rows = [
        ("wal_level",                  "logical",  "",    "postmaster", "supports logical replication"),
        ("synchronous_commit",         "on",       "",    "user",       ""),
        ("full_page_writes",           "on",       "",    "sighup",     ""),
        ("wal_log_hints",              "off",      "",    "postmaster", "required for pg_rewind; enable when using standby failover"),
        ("wal_compression",            "lz4",      "",    "superuser",  ""),
        ("max_wal_senders",            "10",       "",    "postmaster", ""),
        ("max_replication_slots",      "10",       "",    "postmaster", ""),
        ("wal_keep_size",              "256",      "MB",  "sighup",     ""),
        ("max_slot_wal_keep_size",     "-1",       "MB",  "sighup",     ""),
        ("hot_standby",                "on",       "",    "postmaster", ""),
        ("hot_standby_feedback",       "off",      "",    "sighup",     ""),
        ("wal_sender_timeout",         "60000",    "ms",  "sighup",     ""),
        ("archive_mode",               "off",      "",    "postmaster", "WAL archiving disabled; point-in-time recovery unavailable"),
        ("archive_command",            "",         "",    "sighup",     ""),
    ]
    col_styles = [None, None, None, None, hint_style]
    c.print(make_table(cols, rows, col_styles))
    c.print(footer("↑↓ navigate • r refresh • q back"))
    save("replication_config", c)


# ── Database Sizes ─────────────────────────────────────────────────────────────

def gen_database_sizes():
    c = make_console()
    c.print(header("Database Sizes"))

    cols = [("Database", 25), ("Owner", 15), ("Encoding", 10), ("Size", 15)]
    rows = [
        ("production",  "postgres", "UTF8", "48 GB"),
        ("analytics",   "postgres", "UTF8", "12 GB"),
        ("mydb",        "postgres", "UTF8", "890 MB"),
        ("testdb",      "postgres", "UTF8", "42 MB"),
    ]
    c.print(make_table(cols, rows))
    c.print()
    c.print(f"  {label('Total across databases:')} {val('61 GB')}")
    c.print()
    c.print(f"  {label('Tablespaces:')}")
    c.print(f"    {val('pg_default')} {val('60.5 GB')}  {label('(default PGDATA)')}")
    c.print(f"    {val('pg_global')}  {val('612 kB')}   {label('(default PGDATA)')}")
    c.print(footer("r refresh • q back"))
    save("database_sizes", c)


# ── Temp Files ──────────────────────────────────────────────────────────────────

def gen_temp_files():
    c = make_console()
    c.print(header("Temp File Usage"))
    c.print(dim("  Temp files are created when a query needs more memory than work_mem for "
                "sorts, hashes, or materializations — persistent growth here is a sign "
                "work_mem may be too low."))
    c.print()

    cols = [("Database", 25), ("Temp Files", 12), ("Temp Size", 15), ("Stats Reset", 25)]

    def files_style(v):
        return dim(v) if v == "0" else warn(v)

    rows = [
        ("production", "142", "3.2 GB", "2026-06-01 00:00:00+00"),
        ("analytics",  "18",  "410 MB", "2026-06-01 00:00:00+00"),
        ("mydb",       "0",   "0 bytes", "2026-06-01 00:00:00+00"),
        ("testdb",     "0",   "0 bytes", "2026-06-01 00:00:00+00"),
    ]
    col_styles = [None, files_style, None, None]
    c.print(make_table(cols, rows, col_styles))
    c.print(footer("r refresh • q back"))
    save("temp_files", c)


# ── Memory & Checkpoint Stats ────────────────────────────────────────────────────

def gen_memory_stats():
    c = make_console()
    c.print(header("Memory & Checkpoint Stats"))

    cols = [("Parameter", 25), ("Setting", 15), ("Unit", 6), ("Description", 45)]
    rows = [
        ("shared_buffers",        "16384", "8kB", "Sets the number of shared memory buffers used by the server."),
        ("effective_cache_size",  "524288", "8kB", "Sets the planner's assumption about the total size of the data caches."),
        ("work_mem",              "4096",  "kB",  "Sets the maximum memory to be used for query workspaces."),
        ("maintenance_work_mem",  "65536", "kB",  "Sets the maximum memory to be used for maintenance operations."),
        ("wal_buffers",           "512",   "8kB", "Sets the number of disk-page buffers in shared memory for WAL."),
        ("huge_pages",            "try",   "",    "Use of huge pages on Linux or Windows."),
    ]
    c.print(make_table(cols, rows))
    c.print()
    c.print(f"  {label('Buffer cache hit ratio:')} {ok('99.52%')}")
    c.print()
    c.print(f"  {label('Checkpoints & background writer (since stats reset):')}")
    c.print(f"    {label('Timed:')} {val('81')}   {label('Requested:')} {val('2')}")
    c.print(f"    {label('Buffers checkpoint:')} {val('1,152')}   "
            f"{label('Buffers clean:')} {val('0')}   {label('Buffers backend:')} {val('740')}")
    c.print(footer("r refresh • q back"))
    save("memory_stats", c)


# ── Run all ────────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    print("Generating screenshots...")
    gen_dashboard()
    gen_autovacuum()
    gen_autovacuum_detail()
    gen_freeze()
    gen_replication_slots()
    gen_replication_standbys()
    gen_replication_config()
    gen_database_sizes()
    gen_temp_files()
    gen_memory_stats()
    print("Done.")
