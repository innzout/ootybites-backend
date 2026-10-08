package handlers

import (
	"errors"
	"net/http"

	"github.com/innzout/ootybites/internal/middleware"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/pkg/response"
)

// GameHighScore returns the signed-in customer's best runner score and their
// current position on the league table.
func (h *Handlers) GameHighScore(w http.ResponseWriter, r *http.Request) {
	customerID := middleware.SubjectFrom(r.Context())
	best, err := h.game.HighScore(r.Context(), customerID)
	if err == services.ErrSessionInvalid {
		response.Fail(w, http.StatusUnauthorized, response.CodeUnauthorized, "Please sign in again")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load high score")
		return
	}
	// Rank is a nice-to-have — never fail the whole response over it. It is the
	// SEASON rank, matching the board the player is looking at.
	rank, _ := h.game.SeasonRankOf(r.Context(), customerID)
	response.OK(w, map[string]int{"high_score": best, "rank": rank})
}

// GameLeaderboard is the public league table for the SEASON in progress, not
// all time — a league you can never top because someone set a record a year ago
// gives nobody a reason to play this week.
func (h *Handlers) GameLeaderboard(w http.ResponseWriter, r *http.Request) {
	rows, err := h.game.SeasonLeaderboard(r.Context(), 10)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load the league table")
		return
	}
	response.OK(w, map[string]any{"leaders": rows})
}

type gameScoreBody struct {
	Score int `json:"score"`
	// Ticket from POST /game/start — proves the run began on the server and
	// bounds the score by how long it actually ran.
	Ticket string `json:"ticket"`
}

// GameSubmitScore records a finished run and returns the (possibly new) best.
func (h *Handlers) GameSubmitScore(w http.ResponseWriter, r *http.Request) {
	var b gameScoreBody
	if !decodeJSON(w, r, &b) {
		return
	}
	best, err := h.game.SubmitScore(r.Context(), middleware.SubjectFrom(r.Context()), b.Ticket, b.Score)
	if err == services.ErrSessionInvalid {
		response.Fail(w, http.StatusUnauthorized, response.CodeUnauthorized, "Please sign in again")
		return
	}
	// A bad ticket means the run was not started through the API, was replayed,
	// or the score is impossible for the time elapsed — reject it as a bad
	// request rather than hiding it as a server error.
	if errors.Is(err, services.ErrInvalidRun) {
		response.Fail(w, http.StatusBadRequest, response.CodeValidation, "That run could not be verified.")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not save score")
		return
	}
	response.OK(w, map[string]int{"high_score": best})
}

// GameStartRun opens a run and returns the ticket its score must be submitted
// with. Without this the score endpoint would accept any number.
func (h *Handlers) GameStartRun(w http.ResponseWriter, r *http.Request) {
	ticket, err := h.game.StartRun(middleware.SubjectFrom(r.Context()))
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not start run")
		return
	}
	response.OK(w, map[string]string{"ticket": ticket})
}

// ─── League seasons ──────────────────────────────────────────────────────────

// GameSeason is the public view of the running season: what it is called, what
// the prize is, and when it ends. Drives the storefront's league promo, which
// previously advertised a prize with nothing behind it.
func (h *Handlers) GameSeason(w http.ResponseWriter, r *http.Request) {
	s, err := h.game.CurrentSeason(r.Context())
	if errors.Is(err, services.ErrNoOpenSeason) {
		// Not an error for a visitor — the league is simply between seasons.
		response.OK(w, map[string]any{"season": nil})
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load the league")
		return
	}
	response.OK(w, map[string]any{"season": map[string]any{
		"name":      s.Name,
		"prize":     s.Prize,
		"starts_at": s.StartsAt,
	}})
}

// AdminListSeasons returns every season with its winner (name + phone, so staff
// can actually hand the prize over) and how many players took part.
func (h *Handlers) AdminListSeasons(w http.ResponseWriter, r *http.Request) {
	list, err := h.game.ListSeasons(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load seasons")
		return
	}
	response.OK(w, map[string]any{"seasons": list})
}

type seasonBody struct {
	Name  string `json:"name"`
	Prize string `json:"prize"`
}

// AdminOpenSeason closes the running season and starts a new one.
func (h *Handlers) AdminOpenSeason(w http.ResponseWriter, r *http.Request) {
	var b seasonBody
	if !decodeJSON(w, r, &b) {
		return
	}
	s, err := h.game.OpenSeason(r.Context(), b.Name, b.Prize)
	var st *services.OrderStateError
	if errors.As(err, &st) {
		response.Fail(w, http.StatusBadRequest, response.CodeValidation, st.Message)
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not start the season")
		return
	}
	response.OK(w, map[string]any{"season": s})
}

// AdminCloseSeason ends the running season and freezes its winner.
func (h *Handlers) AdminCloseSeason(w http.ResponseWriter, r *http.Request) {
	s, err := h.game.CloseSeason(r.Context())
	if errors.Is(err, services.ErrNoOpenSeason) {
		response.Fail(w, http.StatusBadRequest, response.CodeValidation, "No season is running.")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not close the season")
		return
	}
	response.OK(w, map[string]any{"season": s})
}
