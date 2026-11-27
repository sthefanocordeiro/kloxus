# Funil de testes com usuários — queries SQL

Queries prontas para medir o funil da semana de testes no Postgres/Supabase de
produção. Elas são **somente leitura**: não escrevem em nenhuma tabela.

## O que cada etapa mede

A unidade do funil é a **conta/e-mail** (`lower(magic_links.email)`), não o
`workspace_id`: uma pessoa pode acabar com mais de um workspace ao entrar de
outro navegador ou depois de logout. Agrupar por e-mail evita contar a mesma
pessoa várias vezes.

| # | Etapa | Fonte SQL | Observação |
| --- | --- | --- | --- |
| 1 | E-mail informado (link solicitado) | `magic_links.created_at` | É o topo mensurável no banco: a pessoa pediu um magic link. |
| 2 | Entrou com e-mail (link confirmado) | `workspaces.claimed_at` | Workspace confirmado pelo magic link. |
| 3 | Criou um Radar | `publications.custom = true` | `custom=false` é conteúdo-template e não conta como Radar criado pelo usuário. |
| 4 | Montou uma pauta | `issues` | No fluxo atual, “Montar pauta →” cria a linha em `issues`. |
| 5 | Exportou um texto | `issue_exports` | Uma linha por formato (`linkedin`, `x`, `html`, `markdown`); a query deduplica por pauta e também lista os formatos. |
| 6 | Voltou em outro dia | `workspaces.last_seen_at` + `magic_links.used_at` | Ver limitação abaixo. |

### Limitação importante do retorno

`workspaces.last_seen_at` guarda só a **última** visita — ele é sobrescrito,
não é um histórico. Portanto a etapa 6 é um **limite inferior**, não uma contagem
completa de todos os dias em que a pessoa voltou. A query marca retorno quando:

- a data UTC de `last_seen_at` é posterior à data UTC do primeiro claim; ou
- existem magic links usados em pelo menos dois dias UTC distintos para o mesmo e-mail.

Isso responde “voltou em outro dia?” (booleano). Não responde “quantos dias
distintos a pessoa voltou?” sem uma futura tabela de eventos de visita.

## Como rodar com segurança (read-only)

1. Use a `DATABASE_URL` de produção do cofre/secret manager. **Não cole a URL,
   senhas nem tokens em issue, PR, log ou arquivo do repo.**
2. Rode dentro de uma transação somente leitura e com timeout. Exemplo com `psql`:

   ```sh
   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -P pager=off <<'SQL'
   BEGIN READ ONLY;
   SET statement_timeout = '10s';

   -- cole aqui a query agregada ou a query por-conta

   ROLLBACK;
   SQL
   ```

3. No SQL Editor do Supabase, use o mesmo envelope `BEGIN READ ONLY; ... ROLLBACK;`.
   Não use a service-role key para isso; as queries falam diretamente com Postgres.
4. A saída por-conta contém e-mails pessoais. Use só para cruzar com a planilha
   de testers do owner; não cole a lista completa em comentários públicos.

## Parâmetros no topo das queries

As duas queries começam com o CTE `params`:

```sql
select
  '2026-12-01 00:00:00'::text as since_text,
  array[]::text[] as excluded_emails,
  array[]::text[] as excluded_domains
```

- `since_text`: início da coorte. O default é o dia dos convites (`01/12/2026`).
  Para validar antes dos convites, rode uma vez com uma data anterior (ex.
  `2026-11-01 00:00:00`) e rotule o resultado como baseline técnico, não como
  funil dos testers.
- `excluded_emails`: e-mails exatos, lowercase, para remover contas do owner/QA.
  Ex.: `array['owner@example.com']::text[]`.
- `excluded_domains`: domínios inteiros de teste. Ex.:
  `array['kloxus.dev','example.com','hackbrazil.dev','euploading.com','resend.dev','x.dev']::text[]`.

## Query 1 — funil agregado

Conta quantas contas chegaram a cada etapa. `pct_da_primeira_etapa` é a
porcentagem sobre o topo do funil — use esta coluna para comparar com o sinal bom
de referência (70% / 60% / 40% / 30% / 25%). `pct_da_etapa_anterior` ajuda a ver
onde a quebra acontece; como “voltou em outro dia” pode acontecer antes ou depois
de exportar, essa coluna pode passar de 100% em funis históricos pequenos.

```sql
with params as (
  select
    '2026-12-01 00:00:00'::text as since_text,
    array[]::text[] as excluded_emails,
    array[]::text[] as excluded_domains
),
link_emails as (
  select
    lower(ml.email) as email,
    min(to_utc_ts(ml.created_at)) as first_link_request_ts
  from magic_links ml
  group by lower(ml.email)
),
cohort as (
  select le.email, le.first_link_request_ts
  from link_emails le
  cross join params p
  where le.first_link_request_ts >= to_utc_ts(p.since_text)
    and le.email <> all (p.excluded_emails)
    and lower(split_part(le.email, '@', 2)) <> all (p.excluded_domains)
),
email_workspaces as (
  select distinct c.email, w.id as workspace_id
  from cohort c
  join magic_links ml on lower(ml.email) = c.email
  join workspaces w on w.id = ml.workspace_id
  union
  select distinct c.email, w.id as workspace_id
  from cohort c
  join workspaces w on lower(w.owner_email) = c.email
),
rollups as (
  select
    c.email,
    min(c.first_link_request_ts) as first_link_request_ts,
    min(to_utc_ts(nullif(w.claimed_at, ''))) as first_claimed_ts,
    max(to_utc_ts(w.last_seen_at)) as last_seen_ts,
    count(distinct w.id) as workspace_count
  from cohort c
  join email_workspaces ew on ew.email = c.email
  join workspaces w on w.id = ew.workspace_id
  group by c.email
),
flags as (
  select
    r.*,
    exists (
      select 1
      from email_workspaces ew
      join publications p on p.workspace_id = ew.workspace_id
      where ew.email = r.email and p.custom
    ) as created_radar,
    exists (
      select 1
      from email_workspaces ew
      join issues i on i.workspace_id = ew.workspace_id
      where ew.email = r.email
    ) as built_pauta,
    exists (
      select 1
      from email_workspaces ew
      join issue_exports e on e.workspace_id = ew.workspace_id
      where ew.email = r.email
    ) as exported_text,
    coalesce(ml.login_days, 0) as login_days
  from rollups r
  left join lateral (
    select count(distinct ((to_utc_ts(nullif(ml.used_at, '')) at time zone 'UTC')::date)) as login_days
    from magic_links ml
    where lower(ml.email) = r.email
      and nullif(ml.used_at, '') is not null
  ) ml on true
),
measures as (
  select
    f.*,
    (
      (f.first_claimed_ts is not null and f.last_seen_ts is not null
       and (f.last_seen_ts at time zone 'UTC')::date > (f.first_claimed_ts at time zone 'UTC')::date)
      or f.login_days >= 2
    ) as returned_other_day
  from flags f
),
agg as (
  select
    count(*)::numeric as email_informed,
    (count(*) filter (where first_claimed_ts is not null))::numeric as claimed,
    (count(*) filter (where created_radar))::numeric as radar,
    (count(*) filter (where built_pauta))::numeric as pauta,
    (count(*) filter (where exported_text))::numeric as exported,
    (count(*) filter (where returned_other_day))::numeric as returned
  from measures
),
stage_counts as (
  select * from (values
    (1, 'E-mail informado (link solicitado)', (select email_informed from agg)),
    (2, 'Entrou com e-mail (link confirmado)', (select claimed from agg)),
    (3, 'Criou um Radar', (select radar from agg)),
    (4, 'Montou uma pauta', (select pauta from agg)),
    (5, 'Exportou um texto', (select exported from agg)),
    (6, 'Voltou em outro dia', (select returned from agg))
  ) as v(ordem, etapa, contas)
)
select
  ordem,
  etapa,
  contas::integer as contas,
  round(100 * contas / nullif(first_value(contas) over (order by ordem), 0), 1) as pct_da_primeira_etapa,
  round(100 * contas / nullif(lag(contas) over (order by ordem), 0), 1) as pct_da_etapa_anterior
from stage_counts
order by ordem;
```

## Query 2 — funil por conta

Uma linha por e-mail, para cruzar com a planilha de testers. Além das datas e
contagens, traz `export_formats` (quais formatos saíram), `workspace_count`
(para detectar fragmentação de conta) e `returned_other_day`.

```sql
with params as (
  select
    '2026-12-01 00:00:00'::text as since_text,
    array[]::text[] as excluded_emails,
    array[]::text[] as excluded_domains
),
link_emails as (
  select
    lower(ml.email) as email,
    min(to_utc_ts(ml.created_at)) as first_link_request_ts
  from magic_links ml
  group by lower(ml.email)
),
cohort as (
  select le.email, le.first_link_request_ts
  from link_emails le
  cross join params p
  where le.first_link_request_ts >= to_utc_ts(p.since_text)
    and le.email <> all (p.excluded_emails)
    and lower(split_part(le.email, '@', 2)) <> all (p.excluded_domains)
),
email_workspaces as (
  select distinct c.email, w.id as workspace_id
  from cohort c
  join magic_links ml on lower(ml.email) = c.email
  join workspaces w on w.id = ml.workspace_id
  union
  select distinct c.email, w.id as workspace_id
  from cohort c
  join workspaces w on lower(w.owner_email) = c.email
),
rollups as (
  select
    c.email,
    min(c.first_link_request_ts) as first_link_request_ts,
    min(to_utc_ts(nullif(w.claimed_at, ''))) as first_claimed_ts,
    max(to_utc_ts(w.last_seen_at)) as last_seen_ts,
    count(distinct w.id) as workspace_count
  from cohort c
  join email_workspaces ew on ew.email = c.email
  join workspaces w on w.id = ew.workspace_id
  group by c.email
),
radar_stats as (
  select
    ew.email,
    count(distinct p.id) as radar_count,
    string_agg(distinct p.name, ' + ' order by p.name) as radar_names
  from email_workspaces ew
  join publications p on p.workspace_id = ew.workspace_id and p.custom
  group by ew.email
),
pauta_stats as (
  select
    ew.email,
    count(distinct i.id) as pauta_count,
    min(to_utc_ts(i.created_at)) as first_pauta_ts
  from email_workspaces ew
  join issues i on i.workspace_id = ew.workspace_id
  group by ew.email
),
export_stats as (
  select
    ew.email,
    count(distinct e.issue_id) as exported_pauta_count,
    count(*) as export_events,
    string_agg(distinct e.format, ',' order by e.format) as export_formats,
    min(to_utc_ts(e.exported_at)) as first_export_ts
  from email_workspaces ew
  join issue_exports e on e.workspace_id = ew.workspace_id
  group by ew.email
),
login_stats as (
  select
    lower(ml.email) as email,
    count(distinct ((to_utc_ts(nullif(ml.used_at, '')) at time zone 'UTC')::date)) as login_days,
    count(*) filter (where nullif(ml.used_at, '') is not null) as used_links,
    count(*) as requested_links
  from magic_links ml
  group by lower(ml.email)
)
select
  r.email,
  r.first_link_request_ts,
  r.first_claimed_ts,
  coalesce(rs.radar_count, 0) as radar_count,
  rs.radar_names,
  coalesce(ps.pauta_count, 0) as pauta_count,
  ps.first_pauta_ts,
  coalesce(xs.exported_pauta_count, 0) as exported_pauta_count,
  coalesce(xs.export_events, 0) as export_events,
  coalesce(xs.export_formats, '') as export_formats,
  xs.first_export_ts,
  r.last_seen_ts,
  coalesce(ls.login_days, 0) as login_days,
  coalesce(ls.used_links, 0) as used_links,
  coalesce(ls.requested_links, 0) as requested_links,
  r.workspace_count,
  (
    (r.first_claimed_ts is not null and r.last_seen_ts is not null
     and (r.last_seen_ts at time zone 'UTC')::date > (r.first_claimed_ts at time zone 'UTC')::date)
    or coalesce(ls.login_days, 0) >= 2
  ) as returned_other_day
from rollups r
left join radar_stats rs on rs.email = r.email
left join pauta_stats ps on ps.email = r.email
left join export_stats xs on xs.email = r.email
left join login_stats ls on ls.email = r.email
order by r.first_link_request_ts, r.email;
```

## Diagnóstico rápido de ruído de testes

Antes de interpretar o baseline, vale olhar os domínios dos e-mails confirmados.
Domínios como `kloxus.dev` e `example.com` normalmente são automação/QA, não
testers reais:

```sql
select
  lower(split_part(owner_email, '@', 2)) as domain,
  count(*) as claimed_workspaces
from workspaces
where owner_email <> ''
group by 1
order by claimed_workspaces desc, domain;
```

## Leitura do sinal bom

Compare `pct_da_primeira_etapa` com a tabela do plano:

| Etapa | Sinal bom |
| --- | ---: |
| Entrou com e-mail | 70% |
| Criou um Radar | 60% |
| Montou uma pauta | 40% |
| Exportou um texto | 30% |
| Voltou em outro dia | 25% |

A etapa 1 (“E-mail informado”) fica em 100% por construção, porque é o topo da
coorte. Se o plano tratar “entrou com e-mail” como topo, ignore a etapa 1 e
normalize as demais por ela; os números brutos continuam na mesma saída.
