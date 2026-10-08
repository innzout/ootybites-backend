package services

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/innzout/ootybites/internal/db"
	"github.com/jackc/pgx/v5"
)

// Game is the mini-game service: it persists each customer's best runner score.
type Game struct {
	db      *db.Pool
	secret  string
	tickets OTPStore // generic TTL string store, reused to burn run tickets
}

// NewGame builds the game service. secret signs run tickets; tickets gives them
// single use (any TTL string store will do — the OTP store is reused rather than
// standing up a second one).
func NewGame(pool *db.Pool, secret string, tickets OTPStore) *Game {
	return &Game{db: pool, secret: secret, tickets: tickets}
}

// ─── Run tickets (anti-cheat) ────────────────────────────────────────────────
//
// POST /game/score used to accept any integer, so the leaderboard — which the
// storefront advertises with a giveaway — could be topped with one curl. A run
// now starts server-side and gets a signed ticket carrying its start time; the
// score is then bounded by how long the run actually lasted.
//
// This is mitigation, not proof: the client still computes the score, so a
// determined cheat can idle and submit a large-but-plausible number. Prize
// winners should still be sanity-checked by a human.

const (
	// Derived from the real game constants rather than guessed:
	// time score is steps/6 at 60 steps/s = 10 pts/s; top speed is 6 px/step
	// (360 px/s) over ~300 px chunks carrying at most ~8 bonus pickups at 25 pts
	// => roughly 240 pts/s. 250 leaves headroom so a genuine great run is never
	// rejected, while still blocking an absurd submission.
	maxPointsPerSecond = 250
	// Flat allowance so very short runs aren't judged too tightly.
	scoreGrace = 300
	// A run shorter than this cannot have produced a meaningful score.
	minRunDuration = 2 * time.Second
	// Ticket lifetime. Also caps the score ceiling: without it an attacker could
	// sit on a ticket for hours and "earn" an arbitrarily large allowance.
	maxRunDuration = 15 * time.Minute
)

// ErrInvalidRun means the ticket was missing, forged, reused, expired, or the
// score is impossible for the time elapsed.
var ErrInvalidRun = errors.New("invalid run")

// StartRun issues a single-use, signed ticket marking the start of a run.
func (s *Game) StartRun(customerID string) (string, error) {
	nonceBytes := make([]byte, 9)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", fmt.Errorf("run ticket: %w", err)
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	body := fmt.Sprintf("%s.%d.%s", customerID, time.Now().UnixMilli(), nonce)
	return body + "." + s.sign(body), nil
}

func (s *Game) sign(body string) string {
	m := hmac.New(sha256.New, []byte(s.secret))
	m.Write([]byte(body))
	return hex.EncodeToString(m.Sum(nil))
}

// verifyRun checks the ticket's signature, owner, age and single use, returning
// how long the run lasted.
func (s *Game) verifyRun(customerID, ticket string) (time.Duration, error) {
	parts := strings.Split(ticket, ".")
	if len(parts) != 4 {
		return 0, ErrInvalidRun
	}
	body := strings.Join(parts[:3], ".")
	// Constant-time compare so the signature can't be probed byte by byte.
	if !hmac.Equal([]byte(parts[3]), []byte(s.sign(body))) {
		return 0, ErrInvalidRun
	}
	if parts[0] != customerID {
		return 0, ErrInvalidRun // ticket minted for someone else
	}
	startMs, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, ErrInvalidRun
	}
	elapsed := time.Since(time.UnixMilli(startMs))
	if elapsed < minRunDuration || elapsed > maxRunDuration {
		return 0, ErrInvalidRun
	}
	// Burn it: one ticket, one score. Without this a single valid ticket could
	// be replayed, and replays get a bigger allowance as they age.
	key := "gameticket:" + parts[2]
	if _, used := s.tickets.Get(key); used {
		return 0, ErrInvalidRun
	}
	s.tickets.Set(key, "1", maxRunDuration)
	return elapsed, nil
}

// HighScore returns the customer's stored best score (0 if they've never played).
func (s *Game) HighScore(ctx context.Context, customerID string) (int, error) {
	var best int
	err := s.db.QueryRow(ctx,
		`SELECT game_high_score FROM customers WHERE id=$1`, customerID).Scan(&best)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrSessionInvalid
	}
	if err != nil {
		return 0, fmt.Errorf("get high score: %w", err)
	}
	return best, nil
}

// LeaderRow is one entry on the public league table.
type LeaderRow struct {
	Rank  int    `json:"rank"`
	Name  string `json:"name"`
	Score int    `json:"score"`
}

// NOTE: the all-time Leaderboard/RankOf pair was removed when the league moved
// to seasons — see game_season.go for SeasonLeaderboard / SeasonRankOf. The
// lifetime best still lives on customers.game_high_score and is shown as the
// player's personal best.

// SubmitScore records a finished run and returns the customer's best score. It
// only raises the stored value (GREATEST), so an average run never lowers it.
// Negative scores are clamped to 0 to keep the column honest.
func (s *Game) SubmitScore(ctx context.Context, customerID, ticket string, score int) (int, error) {
	if score < 0 {
		score = 0
	}
	elapsed, err := s.verifyRun(customerID, ticket)
	if err != nil {
		return 0, err
	}
	if max := int(elapsed.Seconds())*maxPointsPerSecond + scoreGrace; score > max {
		return 0, ErrInvalidRun
	}
	// The lifetime best and the season best move together: a run that counts for
	// one must count for the other, or the league table and "your best" drift.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var best int
	err = tx.QueryRow(ctx,
		`UPDATE customers SET game_high_score = GREATEST(game_high_score, $2)
		 WHERE id=$1 RETURNING game_high_score`, customerID, score).Scan(&best)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrSessionInvalid
	}
	if err != nil {
		return 0, fmt.Errorf("submit score: %w", err)
	}

	// Record against the open season, if one is running. A closed league must
	// not silently swallow scores, but it also must not fail the player's run —
	// their lifetime best is still theirs.
	if _, err := tx.Exec(ctx,
		`INSERT INTO game_season_scores (season_id, customer_id, best_score, achieved_at)
		 SELECT s.id, $1, $2, now() FROM game_seasons s WHERE s.ends_at IS NULL
		 ON CONFLICT (season_id, customer_id) DO UPDATE
		   SET best_score  = GREATEST(game_season_scores.best_score, EXCLUDED.best_score),
		       -- Only move the timestamp when the score actually improved; ties
		       -- on the board are broken by who got there first.
		       achieved_at = CASE WHEN EXCLUDED.best_score > game_season_scores.best_score
		                          THEN EXCLUDED.achieved_at ELSE game_season_scores.achieved_at END`,
		customerID, score); err != nil {
		return 0, fmt.Errorf("submit season score: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("submit score commit: %w", err)
	}
	return best, nil
}
