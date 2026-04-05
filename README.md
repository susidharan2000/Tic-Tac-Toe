# Multiplayer Tic-Tac-Toe with Nakama

A production-ready, real-time multiplayer Tic-Tac-Toe game built with server-authoritative architecture using Nakama as the backend infrastructure.

---

## Live Demo

- **Frontend:** https://ttt-web-client.vercel.app
- **Nakama Server:** https://tic-tac-toe-production-cf31.up.railway.app

---

## Tech Stack

| Layer | Technology |
|---|---|
| Frontend | React.js (Mobile Optimized) |
| Backend | Nakama (Go Plugin) |
| Database | PostgreSQL |
| Frontend Hosting | Vercel |
| Backend Hosting | Railway |

---

### Design Philosophy

The frontend is UI only — it has zero game logic. It is responsible for:
- Connecting to Nakama via WebSocket
- Joining a match
- Sending moves `{ row, col }`
- Listening for server events
- Rendering the board and game result

The backend (Nakama Go plugin) is the source of truth and is responsible for:
- Move validation
- State management
- Turn enforcement
- Win/draw detection
- Broadcasting updates to clients

---

## Game Model
```go
type TicTacToeMatch struct {
    // Board State
    Board       [3][3]int

    // Player Management
    players     map[string]int
    usernames   map[string]string
    presenceMap map[string]runtime.Presence

    // Game State
    turn          int
    moves         int
    status        string
    initialized   bool

    // Timer
    turnStartTime int64
    turnDuration  int64

    // Win Detection Counters
    rowTracker   [3]int
    colTracker   [3]int
    diagonal     int
    antiDiagonal int
}
```

### Field Breakdown

| Field | Type | Purpose |
|---|---|---|
| `Board` | `[3][3]int` | Game board — 0 empty, 1 X, 2 O |
| `players` | `map[string]int` | Maps userId to role (1 or 2) |
| `usernames` | `map[string]string` | Maps userId to display name |
| `presenceMap` | `map[string]Presence` | Maps userId to Nakama presence for targeted broadcast |
| `turn` | `int` | Current player turn (1 or 2) |
| `moves` | `int` | Total moves played — used for draw detection |
| `status` | `string` | Match lifecycle state |
| `initialized` | `bool` | Guards against double initialization |
| `turnStartTime` | `int64` | Unix timestamp of turn start — used for timer sync |
| `turnDuration` | `int64` | Max seconds per turn (30) |
| `rowTracker` | `[3]int` | Incremental row sums for O(1) win detection |
| `colTracker` | `[3]int` | Incremental column sums for O(1) win detection |
| `diagonal` | `int` | Main diagonal sum for O(1) win detection |
| `antiDiagonal` | `int` | Anti-diagonal sum for O(1) win detection |

### Match Lifecycle
```
INIT → WAITING → PLAYING → FINISHED
```

- `INIT` — match created, maps initialized
- `WAITING` — first player joined, waiting for opponent
- `PLAYING` — both players joined, game in progress
- `FINISHED` — win, draw, or player disconnect

---

### Game Flow

1. Player enters username and clicks Find Match
2. Client joins Nakama matchmaker queue
3. When 2 players are found, Nakama triggers match creation via `RegisterMatchmakerMatched` method
4. Both players join the match and receive role (X or O) via opcode 50
5. Players alternate sending moves via opcode 1
6. Server validates each move, updates board state, checks win/draw
7. Server broadcasts structured game state to all clients
8. Game ends on win, draw, timeout, or disconnect

---

## Core Features

### Server-Authoritative Game Logic
- All game state managed server-side in Go
- Every move validated before being applied
- Client cannot manipulate state — server is the single source of truth
- Structured JSON events broadcast to all connected clients

### Matchmaking System
- Players join a matchmaker queue on clicking Find Match
- Nakama automatically pairs 2 compatible players
- Match created via `RegisterMatchmakerMatched` hook
- Player disconnections handled gracefully — opponent wins immediately

### Timer-Based Game Mode (Bonus)
- 30 second turn timer enforced server-side
- Timer resets on every valid move
- Turn switches automatically on timeout
- Countdown timer displayed in UI, synced with server timestamp

### Concurrent Game Support (Bonus)
- Each match runs as an isolated in-memory actor
- Nakama executes each `MatchLoop` in a single goroutine per match
- No shared state across matches — zero race conditions
- Scales horizontally across multiple concurrent sessions

---

## Move Validation

Every move is rejected if:
- Game status is not `playing`
- It is not the player's turn
- The cell is already occupied
- Row/col is out of bounds

Idempotency: duplicate moves are silently rejected since the cell will already be filled.

---

## Win Detection — O(1) Incremental Counters

Most implementations scan the entire board after every move. That is the wrong approach.

### Naive Approach

After every move:
- Scan all rows
- Scan all columns
- Scan both diagonals

**Time complexity: O(n²)**

---

### Optimized Approach — Incremental Counters 

Win detection is implemented using incremental counters (row, column, diagonal, anti-diagonal) with constant-time O(1) updates per move.

### Data Structures
```
rowTracker[3]   — running sum for each row
colTracker[3]   — running sum for each column
diagonal        — running sum for main diagonal (top-left → bottom-right)
antiDiagonal    — running sum for anti-diagonal (top-right → bottom-left)
```

### Player Encoding
```
Player 1 (X) → +1
Player 2 (O) → -1
```

### On Every Move at position (row = i, col = j)
```
rowTracker[i] += value
colTracker[j] += value

if (i == j)
    diagonal += value

if (i + j == 2)
    antiDiagonal += value
```

### Win Condition
```
abs(rowTracker[i]) == 3   → row win
abs(colTracker[j]) == 3   → column win
abs(diagonal)      == 3   → diagonal win
abs(antiDiagonal)  == 3   → anti-diagonal win
```

That's it. Constant time. No board scan.

---

### Why This Works

```
Row = [X, X, X]  →  +1 +1 +1 = +3  →  abs(3) == 3  →  Player 1 wins
Row = [O, O, O]  →  -1 -1 -1 = -3  →  abs(3) == 3  →  Player 2 wins
Row = [X, O, X]  →  +1 -1 +1 = +1  →  abs(1) != 3  →  no winner
```

**Time complexity: O(1) per move**  
**Space complexity: O(n) for counters + O(n²) for board**

The board is retained for:
- Move validation (checking occupied cells)
- Client rendering (UI state)
- Debugging and traceability

---

## Concurrency Model

Each match runs as an isolated execution unit (actor model):
```
Match A → Goroutine 1
Match B → Goroutine 2
Match C → Goroutine 3
```

- `MatchLoop` is single-threaded per match
- All player messages processed sequentially
- No mutexes or locks needed inside a match
- Eliminates race conditions — deterministic state updates

---

## Opcode Reference

| Opcode | Direction | Description |
|---|---|---|
| 1 | Client → Server | Player move `{ row, col }` |
| 10 | Server → Client | Move applied, board update |
| 20 | Server → Client | Game over, winner declared |
| 30 | Server → Client | Draw |
| 40 | Server → Client | Opponent left, you win |
| 50 | Server → Client | Match init — role, board, players, timer |
| 60 | Server → Client | Turn timeout, turn switched |

---

## Project Structure
```
Frontend (ttt-web-client)
├── src/
│   ├── App.jsx          # Nakama connection, game state management
│   ├── LobbyPage.jsx    # Username input, matchmaking screen
│   ├── GamePage.jsx     # Game board, player info, result screen
│   └── TimerBar.jsx     # Countdown timer synced with server

Backend (Tic-Tac-Toe)
└── main.go              # Nakama Go plugin — match handler, game logic
```

---

## Setup and Installation

### Prerequisites
- Node.js 18+
- Go 1.21+
- Docker

### Run Frontend Locally
```bash
git clone https://github.com/susidharan2000/ttt-web-client
cd ttt-web-client
npm install
npm run dev
```

### Run Backend Locally
```bash
git clone https://github.com/susidharan2000/Tic-Tac-Toe
cd Tic-Tac-Toe

# Start PostgreSQL
docker run -d --name postgres \
  -e POSTGRES_PASSWORD=localdb \
  -e POSTGRES_DB=nakama \
  -p 5432:5432 postgres:14

# Build plugin
docker run --rm \
  -v $(pwd):/nakama/data/modules \
  -w /nakama/data/modules \
  heroiclabs/nakama-pluginbuilder:3.38.0 \
  build -buildmode=plugin -trimpath \
  -o /nakama/data/modules/module.so ./

# Start Nakama
docker run --name nakama \
  --link postgres:postgres \
  -p 7350:7350 \
  -p 7351:7351 \
  -v $(pwd):/nakama/data/modules \
  heroiclabs/nakama:3.38.0 \
  --database.address postgres:localdb@postgres:5432/nakama
```

---

## Deployment

### Backend — Railway

1. Push Go plugin code to GitHub
2. Create new project on Railway → Deploy from GitHub repo
3. Railway auto-detects Dockerfile and builds
4. Add PostgreSQL database service
5. Set environment variable in Tic-Tac-Toe service:
```
DATABASE_URL=postgres:password@host:port/railway
```
6. Generate public domain: Settings → Networking → Generate Domain

### Frontend — Vercel

1. Push React code to GitHub
2. Connect repo to Vercel — auto-detects Vite
3. Every push to `main` triggers auto-deployment

---

## How to Test Multiplayer

1. Open https://ttt-web-client.vercel.app in **two browser tabs**
2. Enter different usernames in each tab
3. Click **Join Match** in both tabs simultaneously
4. Both players are matched automatically
5. Take turns clicking cells to play
6. **Test disconnect:** close one tab — opponent wins immediately
7. **Test timer:** wait 30 seconds without moving — turn switches automatically

---

## API Configuration

| Parameter | Value |
|---|---|
| Server Key | defaultkey |
| HTTP Port | 7350 |
| Console Port | 7351 |
| Console Login | admin / admin |
| Token Expiry | 7200 seconds |

---

## Design Decisions

### A) Matchmaking — Nakama Built-in vs Custom Queue

Used Nakama's built-in matchmaker instead of a custom queue.

**Reasoning:** Custom matchmaking is a distributed queue problem with concurrency, fairness, and scaling concerns. Nakama provides a battle-tested solution — using it allows focusing on core game logic instead of reinventing that layer.

**Trade-off:** Less control over matching logic, but highly scalable and reliable.

### B) State Management — In-Memory

Game state stored in-memory, no database writes per move.

**Reasoning:** Low latency and real-time responsiveness. Acceptable trade-off for short-lived casual games where state loss on crash is tolerable.

### C) Win Detection — O(1) Incremental Counters

Used signed counters instead of board scans.

**Reasoning:** Avoids O(n²) scans after every move. Enables constant-time win detection while retaining the board for move validation and client rendering.

### D) Concurrency — Actor Model

Each match isolated in its own goroutine.

**Reasoning:** Eliminates need for locks or mutexes inside match logic. Sequential processing within a match guarantees deterministic state updates with no race conditions.