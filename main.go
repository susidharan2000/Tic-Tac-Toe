package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/heroiclabs/nakama-common/runtime"
)

type TicTacToeMatch struct {
	Board       [3][3]int
	players     map[string]int
	usernames   map[string]string
	presenceMap map[string]runtime.Presence

	turn          int
	moves         int
	status        string
	initialized   bool
	turnStartTime int64 // unix seconds
	turnDuration  int64 // seconds

	rowTracker   [3]int
	colTracker   [3]int
	diagonal     int
	antiDiagonal int
}

type MoveEvent struct {
	Event         string    `json:"event"`
	Row           int       `json:"row"`
	Col           int       `json:"col"`
	Board         [3][3]int `json:"board"`
	Turn          int       `json:"turn"`
	TurnStartTime int64     `json:"turnStartTime"`
}

type GameEndEvent struct {
	Event  string    `json:"event"`
	Winner int       `json:"winner"`
	Board  [3][3]int `json:"board"`
}

type TimerEvent struct {
	Event         string `json:"event"`
	Turn          int    `json:"turn"`
	TurnStartTime int64  `json:"turnStartTime"`
	TurnDuration  int64  `json:"turnDuration"`
}

func InitModule(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {
	logger.WithField("module", "tictactoe").Info("MODULE LOADED")

	if err := initializer.RegisterMatch("tictactoe_match",
		func(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule) (runtime.Match, error) {
			return &TicTacToeMatch{}, nil
		}); err != nil {
		logger.WithField("error", err).Error("Failed to register match handler")
		return err
	}

	if err := initializer.RegisterMatchmakerMatched(func(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, entries []runtime.MatchmakerEntry) (string, error) {
		matchId, err := nk.MatchCreate(ctx, "tictactoe_match", map[string]interface{}{})
		if err != nil {
			logger.WithField("error", err).Error("Failed to create match")
			return "", err
		}
		logger.WithField("match_id", matchId).Info("Match created")
		return matchId, nil
	}); err != nil {
		logger.WithField("error", err).Error("Failed to register matchmaker hook")
		return err
	}

	return nil
}

func (t *TicTacToeMatch) MatchInit(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, params map[string]interface{}) (interface{}, int, string) {
	t.players = make(map[string]int)
	t.usernames = make(map[string]string)
	t.presenceMap = make(map[string]runtime.Presence)
	t.turn = 1
	t.turnDuration = 30
	t.turnStartTime = 0
	t.status = "waiting"

	logger.Info("Match initialized")
	return t, 1, `{"mode":"tictactoe"}`
}

func (t *TicTacToeMatch) MatchJoinAttempt(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presence runtime.Presence, metadata map[string]string) (interface{}, bool, string) {
	s := state.(*TicTacToeMatch)
	if len(s.players) >= 2 {
		return s, false, "match full"
	}
	return s, true, ""
}

func (t *TicTacToeMatch) MatchJoin(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presences []runtime.Presence) interface{} {
	s := state.(*TicTacToeMatch)

	for _, p := range presences {
		userID := p.GetUserId()
		username := p.GetUsername()
		s.presenceMap[userID] = p
		s.usernames[userID] = username

		if _, exists := s.players[userID]; exists {
			continue
		}
		if len(s.players) == 0 {
			s.players[userID] = 1
		} else if len(s.players) == 1 {
			s.players[userID] = 2
		}
	}

	if len(s.players) == 2 && !s.initialized {
		s.initialized = true
		s.status = "playing"
		s.turnStartTime = time.Now().Unix()

		for userID, p := range s.presenceMap {
			role := s.players[userID]
			payload := map[string]interface{}{
				"event":         "init",
				"role":          role,
				"board":         s.Board,
				"turn":          s.turn,
				"players":       s.usernames,
				"yourId":        userID,
				"turnStartTime": s.turnStartTime,
				"turnDuration":  s.turnDuration,
			}
			bytes, _ := json.Marshal(payload)
			dispatcher.BroadcastMessage(50, bytes, []runtime.Presence{p}, nil, true)
		}

		logger.Info("Match started")
	}

	return s
}

func (t *TicTacToeMatch) MatchLoop(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, messages []runtime.MatchData) interface{} {
	s := state.(*TicTacToeMatch)

	if s.status != "playing" {
		return s
	}

	// Check turn timer on every tick
	now := time.Now().Unix()
	elapsed := now - s.turnStartTime
	if s.turnStartTime > 0 && elapsed >= s.turnDuration {
		// Time up — switch turn
		logger.WithFields(map[string]interface{}{
			"expired_turn": s.turn,
			"elapsed":      elapsed,
		}).Info("Turn timed out, switching turn")

		if s.turn == 1 {
			s.turn = 2
		} else {
			s.turn = 1
		}
		s.turnStartTime = time.Now().Unix()

		timerEvent := TimerEvent{
			Event:         "turn_timeout",
			Turn:          s.turn,
			TurnStartTime: s.turnStartTime,
			TurnDuration:  s.turnDuration,
		}
		bytes, _ := json.Marshal(timerEvent)
		dispatcher.BroadcastMessage(60, bytes, nil, nil, true)
	}

	// Process moves
	for _, msg := range messages {
		if s.status != "playing" {
			return s
		}
		if msg.GetOpCode() != 1 {
			continue
		}
		userID := msg.GetUserId()
		player, ok := s.players[userID]
		if !ok || player != s.turn {
			continue
		}
		var move struct {
			Row int `json:"row"`
			Col int `json:"col"`
		}
		if err := json.Unmarshal(msg.GetData(), &move); err != nil {
			continue
		}
		//validate the move
		if move.Row < 0 || move.Row >= 3 || move.Col < 0 || move.Col >= 3 || s.Board[move.Row][move.Col] != 0 {
			continue
		}
		s.Board[move.Row][move.Col] = player
		s.moves++
		// player 1 : +1 else player2 : -1
		val := 1
		if player == 2 {
			val = -1
		}
		s.rowTracker[move.Row] += val
		s.colTracker[move.Col] += val
		if move.Row == move.Col {
			s.diagonal += val
		}
		if move.Row+move.Col == 2 {
			s.antiDiagonal += val
		}

		// Switch turn and reset timer
		if s.turn == 1 {
			s.turn = 2
		} else {
			s.turn = 1
		}
		s.turnStartTime = time.Now().Unix()

		// Broadcast move with new turnStartTime
		event := MoveEvent{
			Event:         "move_played",
			Row:           move.Row,
			Col:           move.Col,
			Board:         s.Board,
			Turn:          s.turn,
			TurnStartTime: s.turnStartTime,
		}
		bytes, _ := json.Marshal(event)
		dispatcher.BroadcastMessage(10, bytes, nil, nil, true)

		// Check win / draw
		if abs(s.rowTracker[move.Row]) == 3 ||
			abs(s.colTracker[move.Col]) == 3 ||
			abs(s.diagonal) == 3 ||
			abs(s.antiDiagonal) == 3 {

			s.status = "finished"
			endEvent := GameEndEvent{
				Event:  "game_finished",
				Winner: player,
				Board:  s.Board,
			}
			endBytes, _ := json.Marshal(endEvent)
			dispatcher.BroadcastMessage(20, endBytes, nil, nil, true)
			logger.WithField("winner", player).Info("Game finished: player won")
			return s
		}

		// Check draw
		if s.moves == 9 {
			s.status = "finished"
			drawBytes, _ := json.Marshal(map[string]string{"event": "draw"})
			dispatcher.BroadcastMessage(30, drawBytes, nil, nil, true)
			logger.Info("Game finished: draw")
			return s
		}
	}

	return s
}

func (t *TicTacToeMatch) MatchLeave(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presences []runtime.Presence) interface{} {
	s := state.(*TicTacToeMatch)

	for _, p := range presences {
		userID := p.GetUserId()
		delete(s.players, userID)
		delete(s.presenceMap, userID)
		logger.WithField("user_id", userID).Info("Player left match")
	}

	if s.status == "playing" && len(s.players) == 1 {
		var winner int
		for _, role := range s.players {
			winner = role
			break
		}
		payload := map[string]interface{}{
			"event":  "player_left",
			"winner": winner,
		}
		bytes, _ := json.Marshal(payload)
		dispatcher.BroadcastMessage(40, bytes, nil, nil, true)
		s.status = "finished"
		logger.WithField("winner", winner).Info("Game finished: opponent left")
	}

	if len(s.players) == 0 {
		s.status = "waiting"
	}

	return s
}

func (t *TicTacToeMatch) MatchSignal(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, data string) (interface{}, string) {
	return state, ""
}

func (t *TicTacToeMatch) MatchTerminate(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, graceSeconds int) interface{} {
	logger.WithField("grace_seconds", graceSeconds).Info("Match terminating")
	return state
}

func abs(val int) int {
	if val < 0 {
		return -val
	}
	return val
}
