package game

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxStudentsPerRoom = 50

type Player struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Avatar    string `json:"avatar"`
	Position  int    `json:"position"`
	Score     int    `json:"score"`
	Connected bool   `json:"connected"`
	Host      bool   `json:"host"`
	Attempts  int    `json:"attempts"`
	Correct   int    `json:"correct"`
	RoundWins int    `json:"roundWins"`
}
type Submission struct {
	PlayerID    string `json:"playerId"`
	Answer      string `json:"answer"`
	Correct     *bool  `json:"correct,omitempty"`
	SubmittedAt int64  `json:"submittedAt"`
}
type Room struct {
	Code           string       `json:"code"`
	GameID         string       `json:"gameId"`
	Game           GameInfo     `json:"game"`
	StageIndex     int          `json:"stageIndex"`
	Phase          string       `json:"phase"`
	Players        []*Player    `json:"players"`
	Turn           int          `json:"turn"`
	TeamStars      int          `json:"teamStars"`
	TargetStars    int          `json:"targetStars"`
	Round          int          `json:"round"`
	LastRoll       int          `json:"lastRoll"`
	Current        *Card        `json:"current,omitempty"`
	ActivePlayerID string       `json:"activePlayerId,omitempty"`
	Submissions    []Submission `json:"submissions"`
	Message        string       `json:"message"`
	WinnerIDs      []string     `json:"winnerIds,omitempty"`
	RoundWinnerID  string       `json:"roundWinnerId,omitempty"`
	RoundEndsAt    int64        `json:"roundEndsAt,omitempty"`
	LobbyStartsAt  int64        `json:"lobbyStartsAt,omitempty"`
	UpdatedAt      time.Time    `json:"updatedAt"`
	clients        map[chan []byte]struct{}
	usedQuestions  map[string]bool
	usedMissions   map[string]bool
}
type Hub struct {
	mu    sync.RWMutex
	rooms map[string]*Room
}
type joinRequest struct {
	Name     string `json:"name"`
	Avatar   string `json:"avatar"`
	Code     string `json:"code"`
	PlayerID string `json:"playerId"`
}
type actionRequest struct {
	PlayerID       string `json:"playerId"`
	Action         string `json:"action"`
	Answer         string `json:"answer"`
	Approved       bool   `json:"approved"`
	GameID         string `json:"gameId"`
	TargetPlayerID string `json:"targetPlayerId"`
}

func NewHub() *Hub { return &Hub{rooms: map[string]*Room{}} }
func (h *Hub) RegisterRoutes(m *http.ServeMux) {
	m.HandleFunc("/api/rooms", h.roomsHandler)
	m.HandleFunc("/api/rooms/", h.roomHandler)
}

func (h *Hub) roomsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "ใช้ POST เท่านั้น")
		return
	}
	var req joinRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, "ข้อมูลไม่ถูกต้อง")
		return
	}
	name := cleanName(req.Name)
	if name == "" {
		writeError(w, 400, "กรุณาใส่ชื่อ")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	code := h.uniqueCode()
	player := newPlayer(name, req.Avatar, true)
	room := &Room{Code: code, GameID: "escape", Game: gameCatalog["escape"], StageIndex: 0, Phase: "lobby", Players: []*Player{player}, TargetStars: 60, Message: "ชวนเพื่อนเข้าห้อง แล้วเริ่มทัวร์นาเมนต์ 5 ด่าน", UpdatedAt: time.Now(), clients: map[chan []byte]struct{}{}, usedQuestions: map[string]bool{}, usedMissions: map[string]bool{}}
	h.rooms[code] = room
	writeJSON(w, 201, map[string]any{"room": room, "playerId": player.ID})
}

func (h *Hub) roomHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/rooms/"), "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, 404, "ไม่พบห้อง")
		return
	}
	code := strings.ToUpper(parts[0])
	if len(parts) == 2 && parts[1] == "events" {
		h.events(w, r, code)
		return
	}
	if len(parts) == 2 && parts[1] == "join" {
		h.join(w, r, code)
		return
	}
	if len(parts) == 2 && parts[1] == "action" {
		h.action(w, r, code)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		h.mu.RLock()
		room := h.rooms[code]
		if room == nil {
			h.mu.RUnlock()
			writeError(w, 404, "ไม่พบห้องนี้")
			return
		}
		data := snapshot(room)
		h.mu.RUnlock()
		writeJSON(w, 200, data)
		return
	}
	writeError(w, 404, "ไม่พบเส้นทาง")
}

func (h *Hub) join(w http.ResponseWriter, r *http.Request, code string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "ใช้ POST เท่านั้น")
		return
	}
	var req joinRequest
	if readJSON(r, &req) != nil {
		writeError(w, 400, "ข้อมูลไม่ถูกต้อง")
		return
	}
	h.mu.Lock()
	room := h.rooms[code]
	if room == nil {
		h.mu.Unlock()
		writeError(w, 404, "ไม่พบห้องนี้")
		return
	}
	if req.PlayerID != "" {
		for _, p := range room.Players {
			if p.ID == req.PlayerID {
				p.Connected = true
				data := snapshot(room)
				h.mu.Unlock()
				writeJSON(w, 200, map[string]any{"room": data, "playerId": p.ID})
				return
			}
		}
	}
	if room.Phase != "lobby" {
		h.mu.Unlock()
		writeError(w, 409, "เกมเริ่มแล้ว รอรอบถัดไปนะ")
		return
	}
	if studentCount(room) >= maxStudentsPerRoom {
		h.mu.Unlock()
		writeError(w, 409, "ห้องเต็มแล้ว (รองรับนักเรียนสูงสุด 50 คน)")
		return
	}
	player := newPlayer(cleanName(req.Name), req.Avatar, false)
	room.Players = append(room.Players, player)
	room.LobbyStartsAt = 0
	room.Message = player.Name + " เข้าร่วมแล้ว — รอครูเริ่มเกม"
	h.broadcastLocked(room)
	data := snapshot(room)
	h.mu.Unlock()
	writeJSON(w, 200, map[string]any{"room": data, "playerId": player.ID})
}

func studentCount(room *Room) int {
	count := 0
	for _, player := range room.Players {
		if !player.Host {
			count++
		}
	}
	return count
}

func (h *Hub) action(w http.ResponseWriter, r *http.Request, code string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "ใช้ POST เท่านั้น")
		return
	}
	var req actionRequest
	if readJSON(r, &req) != nil {
		writeError(w, 400, "ข้อมูลไม่ถูกต้อง")
		return
	}
	h.mu.Lock()
	room := h.rooms[code]
	if room == nil {
		h.mu.Unlock()
		writeError(w, 404, "ไม่พบห้อง")
		return
	}
	actor := findPlayer(room, req.PlayerID)
	if actor == nil {
		h.mu.Unlock()
		writeError(w, 403, "ไม่พบผู้เล่น")
		return
	}
	var err error
	startTimer := false
	earlyResultRound := 0
	switch req.Action {
	case "start":
		err = startGame(room, actor, req.GameID)
		startTimer = err == nil
	case "roll":
		err = roll(room, actor)
	case "draw_challenge":
		err = drawChallenge(room, actor)
	case "answer":
		err = answer(room, actor, req.Answer)
		if err == nil && room.GameID != "classify" && room.RoundWinnerID != "" {
			earlyResultRound = room.Round
			closeRoundLocked(room)
		}
	case "judge":
		err = judge(room, actor, req.Approved)
	case "teacher_judge":
		err = teacherJudge(room, actor, req.TargetPlayerID, req.Approved)
	case "next_round":
		err = nextRound(room, actor)
	case "next":
		err = nextTurn(room, actor)
	case "continue_stage":
		err = continueStage(room, actor)
		startTimer = err == nil
	case "restart":
		err = restart(room, actor)
	default:
		err = errors.New("ไม่รู้จักคำสั่งนี้")
	}
	if err != nil {
		h.mu.Unlock()
		writeError(w, 409, err.Error())
		return
	}
	h.broadcastLocked(room)
	data := snapshot(room)
	h.mu.Unlock()
	writeJSON(w, 200, data)
	if startTimer {
		go h.runAutomaticStage(code)
	}
	if earlyResultRound > 0 {
		go h.runAutomaticResultAdvance(code, earlyResultRound)
	}
}

func startGame(r *Room, p *Player, gameID string) error {
	if !p.Host {
		return errors.New("เฉพาะหัวหน้าห้องเริ่มเกมได้")
	}
	if len(r.Players) < 2 {
		return errors.New("ชวนเพื่อนอย่างน้อย 1 คนก่อนเริ่ม")
	}
	r.StageIndex = 0
	r.GameID = gameOrder[0]
	info := gameCatalog[r.GameID]
	r.Game = info
	r.Phase = "stage_transition"
	r.LobbyStartsAt = 0
	r.Turn = 0
	r.Round = 1
	r.RoundWinnerID = ""
	r.Message = "เตรียมเข้าสู่ด่าน 1/5 “" + info.Name + "”"
	return nil
}
func roll(r *Room, p *Player) error {
	if r.GameID != "board" {
		return errors.New("ด่านนี้ไม่ใช้ลูกเต๋า")
	}
	if r.Phase != "playing" || r.Current != nil {
		return errors.New("ยังทอยตอนนี้ไม่ได้")
	}
	if r.Players[r.Turn].ID != p.ID {
		return errors.New("ยังไม่ถึงตาของเธอ")
	}
	n := secureInt(6) + 1
	r.LastRoll = n
	p.Position += n
	if p.Position > 24 {
		p.Position = 24
	}
	r.ActivePlayerID = p.ID
	kind := cellType(p.Position)
	r.Message = fmt.Sprintf("%s ทอยได้ %d เดินไปช่อง %d", p.Name, n, p.Position)
	switch kind {
	case "question":
		c := draw(cardsForGame(r.GameID, "question"), r.usedQuestions)
		r.Current = &c
	case "mission":
		c := draw(cardsForGame(r.GameID, "mission"), r.usedMissions)
		r.Current = &c
	case "bonus":
		p.Score += 2
		r.TeamStars++
		r.Message += " — พบดาวโบนัส +2 คะแนน และ +1 ดาวทีม!"
	case "back":
		p.Position -= 2
		if p.Position < 0 {
			p.Position = 0
		}
		r.Message += " — รอยเท้าลวง! ถอย 2 ช่อง"
	}
	return nil
}

func drawChallenge(r *Room, p *Player) error {
	if !p.Host {
		return errors.New("เฉพาะครูเปิดโจทย์ได้")
	}
	if r.Phase != "playing" || r.Current != nil {
		return errors.New("ยังเปิดโจทย์ตอนนี้ไม่ได้")
	}
	used := r.usedQuestions
	cards := cardsForGame(r.GameID, "question")
	c := draw(cards, used)
	r.Current = &c
	r.ActivePlayerID = ""
	r.RoundWinnerID = ""
	r.Submissions = []Submission{}
	r.Message = fmt.Sprintf("เปิดโจทย์ %s รอบที่ %d — ทุกคนตอบพร้อมกัน!", c.ID, r.Round)
	return nil
}

func openRoundLocked(r *Room) {
	used := r.usedQuestions
	c := draw(cardsForGame(r.GameID, "question"), used)
	r.Current = &c
	r.ActivePlayerID = ""
	r.RoundWinnerID = ""
	r.Submissions = []Submission{}
	r.RoundEndsAt = time.Now().Add(time.Duration(c.Seconds) * time.Second).UnixMilli()
	r.Phase = "playing"
	r.Message = fmt.Sprintf("โจทย์ %s เปิดแล้ว — ทุกคนตอบพร้อมกัน!", c.ID)
}

func closeRoundLocked(r *Room) {
	if r.Current == nil {
		return
	}
	if r.Current.Open {
		for i := range r.Submissions {
			if r.Submissions[i].Correct == nil {
				ok := false
				r.Submissions[i].Correct = &ok
			}
		}
	}
	r.RoundEndsAt = 0
	r.Phase = "round_result"
	if r.RoundWinnerID == "" {
		r.Message = "หมดเวลา — รอบนี้ยังไม่มีผู้ชนะ"
	} else if winner := findPlayer(r, r.RoundWinnerID); winner != nil {
		r.Message = "ผู้ชนะรอบนี้คือ " + winner.Name + "!"
	}
}

func advanceAutomaticRoundLocked(r *Room) bool {
	r.Current = nil
	r.Submissions = []Submission{}
	r.RoundWinnerID = ""
	r.Round++
	maxRounds := 5
	if r.StageIndex > 0 {
		maxRounds = 3
	}
	if r.Round > maxRounds {
		finish(r)
		return false
	}
	r.Phase = "playing"
	r.Message = fmt.Sprintf("กำลังเปิดโจทย์รอบ %d/%d", r.Round, maxRounds)
	return true
}

func (h *Hub) runAutomaticStage(code string) {
	for {
		h.mu.Lock()
		r := h.rooms[code]
		if r == nil {
			h.mu.Unlock()
			return
		}
		stageIndex := r.StageIndex
		if r.Phase == "stage_transition" {
			h.broadcastLocked(r)
			h.mu.Unlock()
			time.Sleep(3 * time.Second)
			h.mu.Lock()
			r = h.rooms[code]
			if r == nil || r.StageIndex != stageIndex || r.Phase != "stage_transition" {
				h.mu.Unlock()
				return
			}
			r.Phase = "stage_intro"
			r.Message = fmt.Sprintf("อ่านคำชี้แจงด่าน %d ให้พร้อม", r.StageIndex+1)
			h.broadcastLocked(r)
			h.mu.Unlock()
			time.Sleep(12 * time.Second)
			h.mu.Lock()
			r = h.rooms[code]
			if r == nil || r.StageIndex != stageIndex || r.Phase != "stage_intro" {
				h.mu.Unlock()
				return
			}
			r.Phase = "playing"
			r.Message = "เริ่มโจทย์ด่านนี้!"
			h.broadcastLocked(r)
			h.mu.Unlock()
			continue
		}
		if r == nil || r.Phase != "playing" || r.Current != nil {
			h.mu.Unlock()
			return
		}
		openRoundLocked(r)
		round := r.Round
		cardID := r.Current.ID
		duration := time.Until(time.UnixMilli(r.RoundEndsAt))
		h.broadcastLocked(r)
		h.mu.Unlock()

		timer := time.NewTimer(duration)
		<-timer.C

		h.mu.Lock()
		r = h.rooms[code]
		if r == nil || r.Phase != "playing" || r.Round != round || r.Current == nil || r.Current.ID != cardID {
			h.mu.Unlock()
			return
		}
		closeRoundLocked(r)
		h.broadcastLocked(r)
		h.mu.Unlock()

		resultTimer := time.NewTimer(5 * time.Second)
		<-resultTimer.C

		h.mu.Lock()
		r = h.rooms[code]
		if r == nil || r.Phase != "round_result" || r.Round != round {
			h.mu.Unlock()
			return
		}
		if !advanceAutomaticRoundLocked(r) {
			autoNext := r.Phase == "stage_complete"
			h.broadcastLocked(r)
			h.mu.Unlock()
			if autoNext {
				go h.runAutomaticNextStage(code)
			}
			return
		}
		h.broadcastLocked(r)
		h.mu.Unlock()
	}
}

func (h *Hub) runAutomaticResultAdvance(code string, round int) {
	time.Sleep(3 * time.Second)
	h.mu.Lock()
	r := h.rooms[code]
	if r == nil || r.Phase != "round_result" || r.Round != round {
		h.mu.Unlock()
		return
	}
	if !advanceAutomaticRoundLocked(r) {
		autoNext := r.Phase == "stage_complete"
		h.broadcastLocked(r)
		h.mu.Unlock()
		if autoNext {
			go h.runAutomaticNextStage(code)
		}
		return
	}
	h.broadcastLocked(r)
	h.mu.Unlock()
	h.runAutomaticStage(code)
}

func (h *Hub) runAutomaticNextStage(code string) {
	time.Sleep(4 * time.Second)
	h.mu.Lock()
	r := h.rooms[code]
	if r == nil || r.Phase != "stage_complete" {
		h.mu.Unlock()
		return
	}
	var host *Player
	for _, p := range r.Players {
		if p.Host {
			host = p
			break
		}
	}
	if host == nil || continueStage(r, host) != nil {
		h.mu.Unlock()
		return
	}
	h.broadcastLocked(r)
	h.mu.Unlock()
	h.runAutomaticStage(code)
}
func answer(r *Room, p *Player, text string) error {
	if r.Current == nil {
		return errors.New("ไม่มีการ์ดให้ตอบ")
	}
	if r.Phase != "playing" || (r.RoundEndsAt > 0 && time.Now().UnixMilli() >= r.RoundEndsAt) {
		return errors.New("หมดเวลาส่งคำตอบแล้ว")
	}
	if p.Host {
		return errors.New("หน้าครูใช้แสดงคะแนนและไม่ร่วมแข่งขัน")
	}
	for _, s := range r.Submissions {
		if s.PlayerID == p.ID {
			return errors.New("ตอบแล้ว รอผลได้เลย")
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("เลือกหรือพิมพ์คำตอบก่อน")
	}
	p.Attempts++
	s := Submission{PlayerID: p.ID, Answer: text, SubmittedAt: time.Now().UnixMilli()}
	if !r.Current.Open {
		ok := text == r.Current.Answer
		s.Correct = &ok
		if ok {
			p.Correct++
			if r.RoundWinnerID == "" {
				r.RoundWinnerID = p.ID
				p.Score += 3
				p.RoundWins++
				r.TeamStars++
				r.Message = p.Name + " ตอบถูกเป็นคนแรกและชนะรอบนี้! +3 คะแนน"
			} else {
				p.Score++
			}
		}
	} else {
		r.Message = "รับคำตอบข้อเขียนแล้ว กำลังรอครูตรวจ"
	}
	r.Submissions = append(r.Submissions, s)
	return nil
}

func teacherJudge(r *Room, teacher *Player, targetID string, approved bool) error {
	if !teacher.Host {
		return errors.New("เฉพาะครูตรวจข้อเขียนได้")
	}
	if r.Current == nil || !r.Current.Open {
		return errors.New("ไม่มีข้อเขียนให้ตรวจ")
	}
	if r.Phase != "playing" || (r.RoundEndsAt > 0 && time.Now().UnixMilli() >= r.RoundEndsAt) {
		return errors.New("หมดเวลาตรวจรอบนี้แล้ว")
	}
	student := findPlayer(r, targetID)
	if student == nil || student.Host {
		return errors.New("ไม่พบนักเรียน")
	}
	for i := range r.Submissions {
		s := &r.Submissions[i]
		if s.PlayerID != targetID {
			continue
		}
		if s.Correct != nil {
			return errors.New("ครูตรวจคำตอบนี้แล้ว")
		}
		ok := approved
		s.Correct = &ok
		if approved {
			student.Correct++
			if r.RoundWinnerID == "" {
				r.RoundWinnerID = student.ID
				student.Score += 3
				student.RoundWins++
				r.TeamStars++
				r.Message = student.Name + " ผ่านการตรวจเป็นคนแรกและชนะรอบนี้!"
			} else {
				student.Score++
			}
		}
		return nil
	}
	return errors.New("ไม่พบคำตอบที่ต้องการตรวจ")
}

func nextRound(r *Room, teacher *Player) error {
	if !teacher.Host {
		return errors.New("เฉพาะครูไปข้อถัดไปได้")
	}
	if r.Current == nil || len(r.Submissions) == 0 {
		return errors.New("รอให้นักเรียนส่งคำตอบก่อน")
	}
	if r.Current.Open {
		judged := false
		for _, s := range r.Submissions {
			if s.Correct != nil {
				judged = true
				break
			}
		}
		if !judged {
			return errors.New("ครูต้องตรวจอย่างน้อยหนึ่งคำตอบก่อน")
		}
	}
	r.Current = nil
	r.Submissions = []Submission{}
	r.RoundWinnerID = ""
	r.Round++
	maxRounds := 5
	if r.StageIndex > 0 {
		maxRounds = 3
	}
	if r.Round > maxRounds {
		finish(r)
		return nil
	}
	r.Message = fmt.Sprintf("รอบ %d/%d — ครูเปิดโจทย์ข้อต่อไปได้เลย", r.Round, maxRounds)
	return nil
}
func judge(r *Room, p *Player, approved bool) error {
	if r.Current == nil || !r.Current.Open {
		return errors.New("การ์ดนี้ไม่ต้องโหวต")
	}
	if p.ID == r.ActivePlayerID {
		return errors.New("เจ้าของคำตอบโหวตตัวเองไม่ได้")
	}
	for _, s := range r.Submissions {
		if s.PlayerID == p.ID {
			return errors.New("โหวตแล้ว")
		}
	}
	ok := approved
	r.Submissions = append(r.Submissions, Submission{PlayerID: p.ID, Answer: "vote", Correct: &ok})
	yes, no := 0, 0
	for _, s := range r.Submissions {
		if s.Correct != nil {
			if *s.Correct {
				yes++
			} else {
				no++
			}
		}
	}
	if yes+no >= max(1, len(r.Players)-1) {
		active := findPlayer(r, r.ActivePlayerID)
		if yes > no {
			points := 2
			if r.Current.Kind == "mission" {
				points = 3
			}
			active.Score += points
			r.TeamStars++
			r.Message = "เพื่อนนักสืบรับรองคำตอบ! ได้คะแนนและ +1 ดาวทีม"
		} else {
			r.Message = "คำตอบนี้ยังไม่ผ่าน ลองดูแนวทางแล้วฝึกอีกครั้งนะ"
		}
	}
	return nil
}
func nextTurn(r *Room, p *Player) error {
	if !p.Host && p.ID != r.ActivePlayerID {
		return errors.New("ให้ผู้เล่นในตาหรือหัวหน้าห้องไปต่อ")
	}
	if r.Current != nil {
		if len(r.Submissions) == 0 {
			return errors.New("ตอบการ์ดก่อนนะ")
		}
		if r.Current.Open {
			votes := 0
			for _, s := range r.Submissions {
				if s.Correct != nil {
					votes++
				}
			}
			if votes < max(1, len(r.Players)-1) {
				return errors.New("รอเพื่อนช่วยโหวตก่อน")
			}
		}
	}
	if r.GameID != "board" {
		r.Current = nil
		r.Submissions = nil
		r.ActivePlayerID = ""
		r.Turn = (r.Turn + 1) % len(r.Players)
		if r.Turn == 0 {
			r.Round++
		}
		if r.Round > 3 {
			finish(r)
			return nil
		}
		r.Message = fmt.Sprintf("รอบ %d/3 — ถึงตาของ %s", r.Round, r.Players[r.Turn].Name)
		return nil
	}
	if findPlayer(r, r.ActivePlayerID).Position >= 24 {
		finish(r)
		return nil
	}
	r.Current = nil
	r.Submissions = nil
	r.LastRoll = 0
	r.ActivePlayerID = ""
	r.Turn = (r.Turn + 1) % len(r.Players)
	if r.Turn == 0 {
		r.Round++
	}
	r.Message = "ถึงตาของ " + r.Players[r.Turn].Name
	if r.Round > 12 {
		finish(r)
	}
	return nil
}
func finish(r *Room) {
	if r.StageIndex < len(gameOrder)-1 {
		r.Phase = "stage_complete"
		next := gameCatalog[gameOrder[r.StageIndex+1]]
		r.Message = fmt.Sprintf("ผ่านด่าน %d แล้ว! ด่านต่อไป: %s", r.StageIndex+1, next.Name)
		return
	}
	r.Phase = "finished"
	best := -1
	for _, p := range r.Players {
		if p.Host {
			continue
		}
		if p.Score > best {
			best = p.Score
			r.WinnerIDs = []string{p.ID}
		} else if p.Score == best {
			r.WinnerIDs = append(r.WinnerIDs, p.ID)
		}
	}
	if r.TeamStars >= r.TargetStars {
		r.Message = "ภารกิจทีมสำเร็จ! ทุกคนคือจอมปราบประโยค"
	} else {
		r.Message = "ปิดคดีแล้ว! อีกนิดเดียวทีมก็จะเก็บดาวครบ"
	}
}

func continueStage(r *Room, p *Player) error {
	if !p.Host {
		return errors.New("เฉพาะหัวหน้าห้องเปิดด่านถัดไปได้")
	}
	if r.Phase != "stage_complete" || r.StageIndex >= len(gameOrder)-1 {
		return errors.New("ยังไปด่านถัดไปไม่ได้")
	}
	r.StageIndex++
	r.GameID = gameOrder[r.StageIndex]
	r.Game = gameCatalog[r.GameID]
	r.Phase = "stage_transition"
	r.Turn = 0
	r.Round = 1
	r.LastRoll = 0
	r.Current = nil
	r.ActivePlayerID = ""
	r.Submissions = []Submission{}
	r.usedQuestions = map[string]bool{}
	r.usedMissions = map[string]bool{}
	for _, player := range r.Players {
		player.Position = 0
	}
	r.Message = fmt.Sprintf("เตรียมเข้าสู่ด่าน %d/5 “%s”", r.StageIndex+1, r.Game.Name)
	return nil
}
func restart(r *Room, p *Player) error {
	if !p.Host {
		return errors.New("เฉพาะหัวหน้าห้องเริ่มรอบใหม่ได้")
	}
	for _, x := range r.Players {
		x.Position = 0
		x.Score = 0
		x.Attempts = 0
		x.Correct = 0
		x.RoundWins = 0
	}
	r.Phase = "lobby"
	r.StageIndex = 0
	r.GameID = gameOrder[0]
	r.Game = gameCatalog[r.GameID]
	r.Turn = 0
	r.Round = 0
	r.TeamStars = 0
	r.TargetStars = 60
	r.LastRoll = 0
	r.Current = nil
	r.Submissions = nil
	r.WinnerIDs = nil
	r.RoundWinnerID = ""
	r.usedQuestions = map[string]bool{}
	r.usedMissions = map[string]bool{}
	r.Message = "พร้อมเปิดคดีใหม่แล้ว"
	return nil
}

func (h *Hub) events(w http.ResponseWriter, r *http.Request, code string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "ไม่รองรับการเชื่อมต่อสด")
		return
	}
	h.mu.Lock()
	room := h.rooms[code]
	if room == nil {
		h.mu.Unlock()
		writeError(w, 404, "ไม่พบห้อง")
		return
	}
	ch := make(chan []byte, 8)
	room.clients[ch] = struct{}{}
	initial, _ := json.Marshal(snapshot(room))
	h.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprintf(w, "data: %s\n\n", initial)
	flusher.Flush()
	defer func() { h.mu.Lock(); delete(room.clients, ch); close(ch); h.mu.Unlock() }()
	for {
		select {
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-time.After(20 * time.Second):
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
func (h *Hub) broadcastLocked(r *Room) {
	r.UpdatedAt = time.Now()
	data, _ := json.Marshal(snapshot(r))
	for ch := range r.clients {
		select {
		case ch <- data:
		default:
		}
	}
}
func snapshot(r *Room) *Room {
	c := *r
	if c.Submissions == nil {
		c.Submissions = []Submission{}
	}
	c.clients = nil
	c.usedQuestions = nil
	c.usedMissions = nil
	return &c
}
func draw(cards []Card, used map[string]bool) Card {
	if len(used) == len(cards) {
		for k := range used {
			delete(used, k)
		}
	}
	available := []Card{}
	for _, c := range cards {
		if !used[c.ID] {
			available = append(available, c)
		}
	}
	c := available[secureInt(len(available))]
	used[c.ID] = true
	return c
}
func newPlayer(name, avatar string, host bool) *Player {
	avatars := []string{"🦊", "🐼", "🐯", "🐸", "🐰", "🐨", "🦁", "🐧"}
	if avatar == "" {
		avatar = avatars[secureInt(len(avatars))]
	}
	return &Player{ID: randomID(12), Name: name, Avatar: avatar, Host: host, Connected: true}
}
func findPlayer(r *Room, id string) *Player {
	for _, p := range r.Players {
		if p.ID == id {
			return p
		}
	}
	return nil
}
func cleanName(s string) string {
	s = strings.TrimSpace(s)
	rr := []rune(s)
	if len(rr) > 18 {
		rr = rr[:18]
	}
	return string(rr)
}
func (h *Hub) uniqueCode() string {
	for {
		letters := "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
		b := make([]byte, 5)
		for i := range b {
			b[i] = letters[secureInt(len(letters))]
		}
		code := string(b)
		if h.rooms[code] == nil {
			return code
		}
	}
}
func randomID(n int) string {
	letters := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[secureInt(len(letters))]
	}
	return string(b)
}
func secureInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return int(time.Now().UnixNano() % int64(n))
	}
	return int(v.Int64())
}
func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10)).Decode(v)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
