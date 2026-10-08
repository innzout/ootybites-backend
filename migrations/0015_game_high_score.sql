-- =====================================================================
-- Ootybites mini-game ("collect the bites" runner): store each customer's
-- best score so it persists across logins and devices. One value per customer
-- keeps it simple; bump it only when a new run beats the stored best.
-- =====================================================================
alter table customers add column if not exists game_high_score int not null default 0;
