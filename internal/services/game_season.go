package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNoOpenSeason means the league has no season running, so there is nothing
// to score into. Admins open one from the league screen.
var ErrNoOpenSeason = errors.New("no open season")

// Season is a league period: a window with a prize and, once closed, a winner.
type Season struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Prize     *string    `json:"prize"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	WinnerID  *string    `json:"winner_customer_id"`
	WinnerScore *int     `json:"winner_score"`
	// Filled for admin views only — never on the public endpoint.
	WinnerName  *string `json:"winner_name,omitempty"`
	WinnerPhone *string `json:"winner_phone,omitempty"`
	Players     int     `json:"players"`
}

const seasonCols = `id, name, prize, starts_at, ends_at, winner_customer_id, winner_score`

func scanSeason(row pgx.Row) (*Season, error) {
	var s Season
	err := row.Scan(&s.ID, &s.Name, &s.Prize, &s.StartsAt, &s.EndsAt, &s.WinnerID, &s.WinnerScore)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// CurrentSeason returns the open season, or ErrNoOpenSeason.
func (s *Game) CurrentSeason(ctx context.Context) (*Season, error) {
	out, err := scanSeason(s.db.QueryRow(ctx,
		`SELECT `+seasonCols+` FROM game_seasons WHERE ends_at IS NULL LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoOpenSeason
	}
	if err != nil {
		return nil, fmt.Errorf("current season: %w", err)
	}
	return out, nil
}

// ListSeasons returns seasons newest first, with player counts and — for closed
// ones — the winner's name and phone so an admin can actually hand over a prize.
func (s *Game) ListSeasons(ctx context.Context) ([]Season, error) {
	rows, err := s.db.Query(ctx,
		`SELECT s.id, s.name, s.prize, s.starts_at, s.ends_at, s.winner_customer_id, s.winner_score,
		        c.name, c.phone,
		        (SELECT count(*) FROM game_season_scores g WHERE g.season_id = s.id)
		 FROM game_seasons s
		 LEFT JOIN customers c ON c.id = s.winner_customer_id
		 ORDER BY s.starts_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list seasons: %w", err)
	}
	defer rows.Close()

	out := []Season{}
	for rows.Next() {
		var x Season
		if err := rows.Scan(&x.ID, &x.Name, &x.Prize, &x.StartsAt, &x.EndsAt,
			&x.WinnerID, &x.WinnerScore, &x.WinnerName, &x.WinnerPhone, &x.Players); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// OpenSeason closes whatever is running and starts a new season. Closing first
// is required: the unique index allows only one open season at a time.
func (s *Game) OpenSeason(ctx context.Context, name, prize string) (*Season, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &OrderStateError{Message: "A season name is required"}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := closeOpenSeason(ctx, tx); err != nil {
		return nil, err
	}
	var p *string
	if t := strings.TrimSpace(prize); t != "" {
		p = &t
	}
	out, err := scanSeason(tx.QueryRow(ctx,
		`INSERT INTO game_seasons (name, prize) VALUES ($1,$2) RETURNING `+seasonCols, name, p))
	if err != nil {
		return nil, fmt.Errorf("open season: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("open season commit: %w", err)
	}
	return out, nil
}

// CloseSeason ends the running season and freezes its winner.
func (s *Game) CloseSeason(ctx context.Context) (*Season, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx, `SELECT id FROM game_seasons WHERE ends_at IS NULL FOR UPDATE`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoOpenSeason
	}
	if err != nil {
		return nil, fmt.Errorf("close season: %w", err)
	}
	if err := closeOpenSeason(ctx, tx); err != nil {
		return nil, err
	}
	out, err := scanSeason(tx.QueryRow(ctx, `SELECT `+seasonCols+` FROM game_seasons WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("close season commit: %w", err)
	}
	return out, nil
}

// closeOpenSeason stamps ends_at and records the top scorer as the winner. The
// winner is frozen onto the season row rather than recomputed on read, so the
// result cannot drift afterwards. No-op when nothing is open.
func closeOpenSeason(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx,
		`UPDATE game_seasons s
		 SET ends_at = now(),
		     winner_customer_id = w.customer_id,
		     winner_score       = w.best_score
		 FROM (
		   SELECT g.season_id, g.customer_id, g.best_score
		   FROM game_season_scores g
		   JOIN game_seasons o ON o.id = g.season_id AND o.ends_at IS NULL
		   WHERE g.best_score > 0
		   ORDER BY g.best_score DESC, g.achieved_at ASC
		   LIMIT 1
		 ) w
		 WHERE s.id = w.season_id AND s.ends_at IS NULL`)
	if err != nil {
		return fmt.Errorf("freeze winner: %w", err)
	}
	// A season with no scores still has to close, which the join above skips.
	if _, err := tx.Exec(ctx,
		`UPDATE game_seasons SET ends_at = now() WHERE ends_at IS NULL`); err != nil {
		return fmt.Errorf("close season: %w", err)
	}
	return nil
}

// SeasonLeaderboard is the public league table for the open season.
func (s *Game) SeasonLeaderboard(ctx context.Context, limit int) ([]LeaderRow, error) {
	if limit < 1 || limit > 50 {
		limit = 10
	}
	rows, err := s.db.Query(ctx,
		`SELECT coalesce(nullif(btrim(c.name), ''), 'Runner'), g.best_score
		 FROM game_season_scores g
		 JOIN game_seasons s  ON s.id = g.season_id AND s.ends_at IS NULL
		 JOIN customers    c  ON c.id = g.customer_id
		 WHERE g.best_score > 0
		 ORDER BY g.best_score DESC, g.achieved_at ASC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("season leaderboard: %w", err)
	}
	defer rows.Close()

	out := []LeaderRow{}
	for rows.Next() {
		var r LeaderRow
		if err := rows.Scan(&r.Name, &r.Score); err != nil {
			return nil, err
		}
		// First name only — a full name on a public board is more personal data
		// than this feature needs.
		if i := strings.IndexByte(r.Name, ' '); i > 0 {
			r.Name = r.Name[:i]
		}
		r.Rank = len(out) + 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// SeasonRankOf returns the customer's 1-based position in the open season.
func (s *Game) SeasonRankOf(ctx context.Context, customerID string) (int, error) {
	var best int
	err := s.db.QueryRow(ctx,
		`SELECT g.best_score FROM game_season_scores g
		 JOIN game_seasons s ON s.id = g.season_id AND s.ends_at IS NULL
		 WHERE g.customer_id = $1`, customerID).Scan(&best)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil // not on the board yet
	}
	if err != nil {
		return 0, fmt.Errorf("season rank: %w", err)
	}
	var ahead int
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM game_season_scores g
		 JOIN game_seasons s ON s.id = g.season_id AND s.ends_at IS NULL
		 WHERE g.best_score > $1`, best).Scan(&ahead); err != nil {
		return 0, fmt.Errorf("season rank count: %w", err)
	}
	return ahead + 1, nil
}
