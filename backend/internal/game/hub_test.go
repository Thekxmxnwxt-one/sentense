package game

import (
	"testing"
	"time"
)

func testRoom() (*Room, *Player, *Player) {
	a := newPlayer("ครู", "🦊", true)
	b := newPlayer("น้องเมย์", "🐼", false)
	return &Room{Phase: "lobby", Players: []*Player{a, b}, TargetStars: 12, usedQuestions: map[string]bool{}, usedMissions: map[string]bool{}}, a, b
}
func TestStartRequiresPlayers(t *testing.T) {
	r, a, _ := testRoom()
	if err := startGame(r, a, ""); err != nil {
		t.Fatal(err)
	}
	if r.Phase != "playing" {
		t.Fatal("game did not start")
	}
}
func TestTurnProtection(t *testing.T) {
	r, a, b := testRoom()
	_ = startGame(r, a, "")
	if err := drawChallenge(r, b); err == nil {
		t.Fatal("student opened a teacher-controlled challenge")
	}
}
func TestCorrectAnswerScores(t *testing.T) {
	r, _, student := testRoom()
	r.Phase = "playing"
	c := questionCards[0]
	r.Current = &c
	if err := answer(r, student, c.Answer); err != nil {
		t.Fatal(err)
	}
	if student.Score != 3 || student.Correct != 1 || student.RoundWins != 1 || r.TeamStars != 1 {
		t.Fatalf("score=%d correct=%d wins=%d", student.Score, student.Correct, student.RoundWins)
	}
}
func TestCells(t *testing.T) {
	cases := map[int]string{1: "question", 4: "mission", 3: "bonus", 11: "back", 2: "plain"}
	for p, want := range cases {
		if got := cellType(p); got != want {
			t.Fatalf("cell %d got %s", p, got)
		}
	}
}

func TestSnapshotAlwaysUsesSubmissionArray(t *testing.T) {
	r, _, _ := testRoom()
	r.Submissions = nil
	got := snapshot(r)
	if got.Submissions == nil {
		t.Fatal("snapshot returned null submissions")
	}
}

func TestStartSelectsAdvancedGame(t *testing.T) {
	r, host, _ := testRoom()
	if err := startGame(r, host, "classify"); err != nil {
		t.Fatal(err)
	}
	if r.GameID != "escape" || r.StageIndex != 0 || r.Game.Name == "" {
		t.Fatalf("game was not selected: %#v", r.Game)
	}
	for _, card := range cardsForGame("classify", "question") {
		if card.Kind != "question" {
			t.Fatalf("question deck contains %s", card.Kind)
		}
	}
}

func TestCampaignAdvancesAndKeepsScore(t *testing.T) {
	r, host, student := testRoom()
	if err := startGame(r, host, ""); err != nil {
		t.Fatal(err)
	}
	student.Score = 9
	finish(r)
	if r.Phase != "stage_complete" {
		t.Fatalf("phase = %s", r.Phase)
	}
	if err := continueStage(r, host); err != nil {
		t.Fatal(err)
	}
	if r.StageIndex != 1 || r.GameID != "anatomy" || student.Score != 9 {
		t.Fatalf("stage=%d game=%s score=%d", r.StageIndex, r.GameID, student.Score)
	}
}

func TestCampaignFinishesAfterFifthGame(t *testing.T) {
	r, host, other := testRoom()
	r.StageIndex = len(gameOrder) - 1
	r.GameID = gameOrder[r.StageIndex]
	host.Score = 20 // Teacher must be excluded from ranking.
	other.Score = 12
	finish(r)
	if r.Phase != "finished" || len(r.WinnerIDs) != 1 || r.WinnerIDs[0] != other.ID {
		t.Fatalf("unexpected final state: phase=%s winners=%v", r.Phase, r.WinnerIDs)
	}
}

func TestNonBoardStageDrawsChallengeWithoutDice(t *testing.T) {
	r, host, _ := testRoom()
	r.Phase = "playing"
	r.GameID = "anatomy"
	r.Game = gameCatalog["anatomy"]
	r.Round = 1
	r.usedQuestions = map[string]bool{}
	r.usedMissions = map[string]bool{}
	if err := roll(r, host); err == nil {
		t.Fatal("anatomy stage allowed dice roll")
	}
	if err := drawChallenge(r, host); err != nil {
		t.Fatal(err)
	}
	if r.Current == nil || r.ActivePlayerID != "" {
		t.Fatal("challenge was not opened for all students")
	}
}

func TestChallengeStageEndsAfterThreeRounds(t *testing.T) {
	r, host, student := testRoom()
	r.Phase = "playing"
	r.StageIndex = 1
	r.GameID = "anatomy"
	r.Game = gameCatalog["anatomy"]
	r.Round = 3
	r.Current = &Card{ID: "done", Kind: "question"}
	ok := true
	r.Submissions = []Submission{{PlayerID: student.ID, Answer: "x", Correct: &ok}}
	if err := nextRound(r, host); err != nil {
		t.Fatal(err)
	}
	if r.Phase != "stage_complete" {
		t.Fatalf("phase = %s", r.Phase)
	}
}

func TestAutomaticDecksNeverRequireTeacherReview(t *testing.T) {
	for _, gameID := range gameOrder {
		cards := cardsForGame(gameID, "question")
		if len(cards) < 3 {
			t.Fatalf("%s has only %d automatic cards", gameID, len(cards))
		}
		for _, card := range cards {
			if card.Open || card.Answer == "" || len(card.Choices) == 0 {
				t.Fatalf("%s contains non-automatic card %#v", gameID, card)
			}
		}
	}
}

func TestFastestCorrectStudentWinsRound(t *testing.T) {
	r, _, first := testRoom()
	second := newPlayer("น้องนนท์", "🐯", false)
	r.Players = append(r.Players, second)
	r.Phase = "playing"
	card := questionCards[0]
	r.Current = &card
	if err := answer(r, first, card.Answer); err != nil {
		t.Fatal(err)
	}
	if err := answer(r, second, card.Answer); err != nil {
		t.Fatal(err)
	}
	if r.RoundWinnerID != first.ID || first.Score != 3 || second.Score != 1 {
		t.Fatalf("winner=%s first=%d second=%d", r.RoundWinnerID, first.Score, second.Score)
	}
}

func TestSpeedStageCanCloseAsSoonAsThereIsAWinner(t *testing.T) {
	r, _, student := testRoom()
	r.Phase = "playing"
	r.GameID = "escape"
	card := questionCards[0]
	r.Current = &card
	r.RoundEndsAt = time.Now().Add(time.Minute).UnixMilli()
	if err := answer(r, student, card.Answer); err != nil {
		t.Fatal(err)
	}
	if r.RoundWinnerID == "" {
		t.Fatal("speed round has no winner")
	}
	closeRoundLocked(r)
	if r.Phase != "round_result" || r.RoundEndsAt != 0 {
		t.Fatal("speed round did not close immediately")
	}
}

func TestAutomaticRoundLifecycle(t *testing.T) {
	r, _, _ := testRoom()
	r.Phase = "playing"
	r.GameID = "escape"
	r.Game = gameCatalog["escape"]
	r.Round = 1
	openRoundLocked(r)
	if r.Current == nil || r.RoundEndsAt == 0 || r.Phase != "playing" {
		t.Fatal("automatic round did not open")
	}
	closeRoundLocked(r)
	if r.Phase != "round_result" || r.RoundEndsAt != 0 {
		t.Fatal("automatic round did not close")
	}
	if !advanceAutomaticRoundLocked(r) || r.Round != 2 || r.Phase != "playing" {
		t.Fatal("automatic round did not advance")
	}
}
