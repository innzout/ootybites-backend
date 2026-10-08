-- =====================================================================
-- Game league seasons.
--
-- The storefront advertises "play the league, win a hamper", but scores were
-- only ever an all-time column on customers, so there was no period to win and
-- no way to name a winner. A season gives the competition a start, an end and a
-- prize, all editable by an admin rather than hardcoded.
--
-- customers.game_high_score is kept as the lifetime best (shown as "your best");
-- the league table is now driven by per-season rows so closing a season resets
-- the standings without erasing anyone's personal record.
-- =====================================================================

create table if not exists game_seasons (
  id          uuid primary key default gen_random_uuid(),
  name        text        not null,
  -- Free text so the business can describe the prize however it likes
  -- ("Nilgiri hamper", "500 off", ...) without a schema change.
  prize       text,
  starts_at   timestamptz not null default now(),
  -- null = still running. Set when the season is closed.
  ends_at     timestamptz,
  -- Frozen at close time so the winner cannot change afterwards, even if an
  -- old score were ever corrected.
  winner_customer_id uuid references customers(id) on delete set null,
  winner_score       int,
  created_at  timestamptz not null default now()
);

-- At most one season may be open at a time; the service closes the current one
-- before opening the next, and this index makes that a hard guarantee.
create unique index if not exists idx_game_seasons_one_open
  on game_seasons ((ends_at is null)) where ends_at is null;

create index if not exists idx_game_seasons_recent on game_seasons(starts_at desc);

-- One row per customer per season, holding their best run of that season.
create table if not exists game_season_scores (
  season_id   uuid not null references game_seasons(id) on delete cascade,
  customer_id uuid not null references customers(id)    on delete cascade,
  best_score  int  not null default 0,
  achieved_at timestamptz not null default now(),
  primary key (season_id, customer_id)
);

-- Drives the league table: top scores within a season.
create index if not exists idx_game_season_scores_board
  on game_season_scores(season_id, best_score desc, achieved_at asc);

-- Open an initial season so the league is live immediately rather than the
-- storefront advertising a competition that has not started.
insert into game_seasons (name, prize)
select 'Season 1', 'A Nilgiri hamper'
where not exists (select 1 from game_seasons);
