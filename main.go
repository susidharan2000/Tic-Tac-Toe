package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/heroiclabs/nakama-common/runtime"
)

type TicTacToeMatch struct {
	Board         [3][3]int
	players       map[string]int
	usernames     map[string]string
	presenceMap   map[string]runtime.Presence
	turn          int
	moves         int
	status        string
	initialized   bool
	turnStartTime int64
	turnDuration  int64

	//count traker
	rowTracker   [3]int
	colTracker   [3]int
	diagonal     int
	antiDiagonal int
}

type MoveEvent struct {
	Event string    `json:"event"`
	Row   int       `json:"row"`
	Col   int       `json:"col"`
	Board [3][3]int `json:"board"`
	Turn  int       `json:"turn"`
}

type GameEndEvent struct {
	Event  string    `json:"event"`
	Winner int       `json:"winner"`
	Board  [3][3]int `json:"board"`
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
		matchId, err := nk.MatchCreate(ctx, "tictactoe_match", map[string]interface{}{}) //creates the match
		if err != nil {
			logger.WithField("error", err).Error("Failed to create match")
			return "", err
		}
		logger.Info("Match created: %s", matchId)
		return matchId, nil
	}); err != nil {
		logger.Error("Failed to register matchmaker hook")
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
		logger.Info("Join rejected: match full")
		return s, false, "match full"
	}
	logger.Info("Join attempt accepted")
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
			logger.Info("Player id %s joined as X", userID)
		} else if len(s.players) == 1 {
			s.players[userID] = 2
			logger.Info("Player id %s  joined as O", userID)
		}
	}

	//broadCast the state once the match is full
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

			logger.WithFields(map[string]interface{}{
				"user_id": userID,
				"role":    role,
			}).Info("Sent init to player")
		}

		logger.Info("Match started")
	}

	return s
}

func (t *TicTacToeMatch) MatchLoop(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, messages []runtime.MatchData) interface{} {
	s := state.(*TicTacToeMatch)
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
			logger.WithFields(map[string]interface{}{
				"user_id": userID,
				"turn":    s.turn,
			}).Warn("Move rejected: not this player's turn")
			continue
		}
		var move struct {
			Row int `json:"row"`
			Col int `json:"col"`
		}
		if err := json.Unmarshal(msg.GetData(), &move); err != nil {
			logger.WithFields(map[string]interface{}{
				"user_id": userID,
				"error":   err,
			}).Error("Failed to unmarshal move")
			continue
		}
		if move.Row < 0 || move.Row >= 3 || move.Col < 0 || move.Col >= 3 || s.Board[move.Row][move.Col] != 0 {
			logger.WithFields(map[string]interface{}{
				"user_id": userID,
				"row":     move.Row,
				"col":     move.Col,
			}).Warn("Move rejected: invalid cell")
			continue
		}
		s.Board[move.Row][move.Col] = player
		s.moves++
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
		logger.WithFields(map[string]interface{}{
			"user_id": userID,
			"player":  player,
			"row":     move.Row,
			"col":     move.Col,
			"moves":   s.moves,
		}).Info("Move applied")
		// broad cast move
		if s.turn == 1 {
			s.turn = 2
		} else {
			s.turn = 1
		}
		event := MoveEvent{
			Event: "move_played",
			Row:   move.Row,
			Col:   move.Col,
			Board: s.Board,
			Turn:  s.turn,
		}
		bytes, _ := json.Marshal(event)
		dispatcher.BroadcastMessage(10, bytes, nil, nil, true)
		//checking the win condition
		if abs(s.rowTracker[move.Row]) == 3 ||
			abs(s.colTracker[move.Col]) == 3 ||
			abs(s.diagonal) == 3 ||
			abs(s.antiDiagonal) == 3 {

			s.status = "finished"
			event := GameEndEvent{
				Event:  "game_finished",
				Winner: player,
				Board:  s.Board,
			}
			bytes, _ := json.Marshal(event)
			dispatcher.BroadcastMessage(20, bytes, nil, nil, true)

			logger.WithField("winner", player).Info("Game finished: player won")
			return s
		}
		if s.moves == 9 {
			s.status = "finished"
			bytes, _ := json.Marshal(map[string]string{"event": "draw"})
			dispatcher.BroadcastMessage(30, bytes, nil, nil, true)

			logger.Info("Game finished: draw")
			return s
		}
		logger.Info("Turn switched")
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
		logger.Info("Match empty, status reset to waiting")
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
