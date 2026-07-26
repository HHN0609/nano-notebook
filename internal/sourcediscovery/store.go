package sourcediscovery

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/realtime"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound  = errors.New("Source Discovery session not found")
	ErrForbidden = errors.New("Source Discovery requires Source maintenance permission")
	ErrInvalid   = errors.New("invalid Source Discovery input")
	ErrLeaseLost = errors.New("Source Discovery job lease lost")
	ErrState     = errors.New("Source Discovery state conflict")
	ErrCandidate = errors.New("invalid Source Discovery Candidate selection")
)

type Origin string

const (
	OriginManual        Origin = "manual"
	OriginResearchAgent Origin = "research_agent"
)

type Status string

const (
	StatusSearching Status = "searching"
	StatusReady     Status = "ready"
	StatusFailed    Status = "failed"
)

type CandidateStatus string

const (
	CandidateDiscovered   CandidateStatus = "discovered"
	CandidateImporting    CandidateStatus = "importing"
	CandidateImported     CandidateStatus = "imported"
	CandidateImportFailed CandidateStatus = "import_failed"
)

type Session struct {
	ID             string      `json:"id"`
	NotebookID     string      `json:"notebook_id"`
	OriginChatID   *string     `json:"origin_chat_id,omitempty"`
	Origin         Origin      `json:"origin"`
	Query          string      `json:"query"`
	Summary        *string     `json:"summary,omitempty"`
	Status         Status      `json:"status"`
	ErrorCode      *string     `json:"error_code,omitempty"`
	Candidates     []Candidate `json:"candidates"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	CompletedAt    *time.Time  `json:"completed_at,omitempty"`
	ResearchRunID  *string     `json:"-"`
	PrivateOwnerID string      `json:"-"`
}

type Candidate struct {
	ID              string          `json:"id"`
	Ordinal         int             `json:"ordinal"`
	Title           string          `json:"title"`
	CanonicalURL    string          `json:"canonical_url"`
	DisplayURL      string          `json:"display_url"`
	Snippet         string          `json:"snippet"`
	FaviconRef      *string         `json:"favicon_ref,omitempty"`
	Selected        bool            `json:"selected"`
	Status          CandidateStatus `json:"status"`
	SourceID        *string         `json:"source_id,omitempty"`
	ImportErrorCode *string         `json:"import_error_code,omitempty"`
}

type CreateSessionCommand struct {
	ID            string
	JobID         string
	NotebookID    string
	UserID        string
	OriginChatID  *string
	Origin        Origin
	Query         string
	ResearchRunID *string
}

type DiscoveredCandidate struct {
	ID           string
	Title        string
	URL          string
	DisplayURL   string
	Snippet      string
	FaviconRef   *string
	ProviderRank int
}

type CompleteSearchCommand struct {
	SessionID  string
	JobID      string
	LeaseToken string
	Summary    string
	Candidates []DiscoveredCandidate
}

type FailSearchCommand struct {
	SessionID  string
	JobID      string
	LeaseToken string
	ErrorCode  string
}

type ResearchSessionCommand struct {
	ID            string
	NotebookID    string
	UserID        string
	OriginChatID  string
	ResearchRunID string
	Query         string
}

type CandidateImport struct {
	CandidateID string
	NotebookID  string
	Title       string
	URL         string
}

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Store struct {
	db DBTX
}

func NewStore(db DBTX) *Store {
	return &Store{db: db}
}

func (s *Store) CreateSession(ctx context.Context, command CreateSessionCommand) (Session, error) {
	query := strings.TrimSpace(command.Query)
	if command.ID == "" || command.JobID == "" || command.NotebookID == "" || command.UserID == "" ||
		(query == "" || utf8.RuneCountInString(query) > 500) ||
		(command.Origin != OriginManual && command.Origin != OriginResearchAgent) {
		return Session{}, ErrInvalid
	}
	if err := s.requireMaintain(ctx, command.NotebookID); err != nil {
		return Session{}, err
	}
	if command.OriginChatID != nil {
		var chatID string
		if err := s.db.QueryRow(ctx, `
			select id from chat_chats
			where id=$1 and notebook_id=$2 and creator_user_id=$3
		`, *command.OriginChatID, command.NotebookID, command.UserID).Scan(&chatID); errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrInvalid
		} else if err != nil {
			return Session{}, err
		}
	}
	var created Session
	err := s.db.QueryRow(ctx, `
		insert into source_discovery_sessions(
			id,notebook_id,user_id,origin_chat_id,origin,query,status,research_run_id
		) values($1,$2,$3,$4,$5,$6,'searching',$7)
		returning id,notebook_id,user_id,origin_chat_id,origin,query,summary,status,error_code,
			created_at,updated_at,completed_at,research_run_id
	`, command.ID, command.NotebookID, command.UserID, command.OriginChatID, command.Origin, query, command.ResearchRunID).Scan(
		&created.ID, &created.NotebookID, &created.PrivateOwnerID, &created.OriginChatID,
		&created.Origin, &created.Query, &created.Summary, &created.Status, &created.ErrorCode,
		&created.CreatedAt, &created.UpdatedAt, &created.CompletedAt, &created.ResearchRunID,
	)
	if err != nil {
		return Session{}, err
	}
	if _, err := s.db.Exec(ctx, `
		insert into source_discovery_jobs(id,session_id,status) values($1,$2,'queued')
	`, command.JobID, command.ID); err != nil {
		return Session{}, err
	}
	if err := realtime.NotifySourceDiscovery(ctx, s.db, command.ID); err != nil {
		return Session{}, err
	}
	created.Candidates = []Candidate{}
	return created, nil
}

// EnsureResearchSession creates the private candidate workspace owned by a
// Research child Run. Research performs its own bounded provider calls, so no
// separate source_discovery_job is created.
func (s *Store) EnsureResearchSession(ctx context.Context, command ResearchSessionCommand) (Session, error) {
	query := strings.TrimSpace(command.Query)
	if command.ID == "" || command.NotebookID == "" || command.UserID == "" || command.OriginChatID == "" ||
		command.ResearchRunID == "" || query == "" || utf8.RuneCountInString(query) > 500 {
		return Session{}, ErrInvalid
	}
	var sessionID string
	err := s.db.QueryRow(ctx, `
		select id from source_discovery_sessions where research_run_id=$1
	`, command.ResearchRunID).Scan(&sessionID)
	if err == nil {
		return s.GetSession(ctx, sessionID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, err
	}
	var allowed bool
	if err := s.db.QueryRow(ctx, `
		select exists(
			select 1 from chat_chats c
			join notebook_memberships m on m.notebook_id=c.notebook_id
			where c.id=$1 and c.notebook_id=$2 and m.user_id=$3 and m.role in ('owner','editor')
		)
	`, command.OriginChatID, command.NotebookID, command.UserID).Scan(&allowed); err != nil {
		return Session{}, err
	}
	if !allowed {
		return Session{}, ErrForbidden
	}
	if _, err := s.db.Exec(ctx, `
		insert into source_discovery_sessions(
			id,notebook_id,user_id,origin_chat_id,origin,query,status,research_run_id
		) values($1,$2,$3,$4,'research_agent',$5,'searching',$6)
	`, command.ID, command.NotebookID, command.UserID, command.OriginChatID, query, command.ResearchRunID); err != nil {
		return Session{}, err
	}
	if err := realtime.NotifySourceDiscovery(ctx, s.db, command.ID); err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, command.ID)
}

func (s *Store) GetSession(ctx context.Context, sessionID string) (Session, error) {
	var session Session
	err := s.db.QueryRow(ctx, `
		select id,notebook_id,user_id,origin_chat_id,origin,query,summary,status,error_code,
			created_at,updated_at,completed_at,research_run_id
		from source_discovery_sessions where id=$1
	`, sessionID).Scan(
		&session.ID, &session.NotebookID, &session.PrivateOwnerID, &session.OriginChatID,
		&session.Origin, &session.Query, &session.Summary, &session.Status, &session.ErrorCode,
		&session.CreatedAt, &session.UpdatedAt, &session.CompletedAt, &session.ResearchRunID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	rows, err := s.db.Query(ctx, `
		select id,ordinal,title,canonical_url,display_url,snippet,favicon_ref,selected,status,source_id,import_error_code
		from source_discovery_candidates where session_id=$1 order by ordinal,id
	`, sessionID)
	if err != nil {
		return Session{}, err
	}
	defer rows.Close()
	session.Candidates = []Candidate{}
	for rows.Next() {
		var candidate Candidate
		if err := rows.Scan(
			&candidate.ID, &candidate.Ordinal, &candidate.Title, &candidate.CanonicalURL,
			&candidate.DisplayURL, &candidate.Snippet, &candidate.FaviconRef, &candidate.Selected,
			&candidate.Status, &candidate.SourceID, &candidate.ImportErrorCode,
		); err != nil {
			return Session{}, err
		}
		session.Candidates = append(session.Candidates, candidate)
	}
	return session, rows.Err()
}

func (s *Store) LatestSession(ctx context.Context, notebookID string) (Session, error) {
	var sessionID string
	if err := s.db.QueryRow(ctx, `
		select id from source_discovery_sessions
		where notebook_id=$1
		order by created_at desc,id desc limit 1
	`, notebookID).Scan(&sessionID); errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	} else if err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, sessionID)
}

func (s *Store) ReplaceSelection(ctx context.Context, sessionID string, candidateIDs []string) (Session, error) {
	if strings.TrimSpace(sessionID) == "" {
		return Session{}, ErrInvalid
	}
	var status Status
	if err := s.db.QueryRow(ctx, `
		select status from source_discovery_sessions where id=$1 for update
	`, sessionID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	} else if err != nil {
		return Session{}, err
	}
	if status != StatusReady {
		return Session{}, ErrState
	}
	unique := make(map[string]struct{}, len(candidateIDs))
	for _, candidateID := range candidateIDs {
		candidateID = strings.TrimSpace(candidateID)
		if candidateID == "" {
			return Session{}, ErrCandidate
		}
		unique[candidateID] = struct{}{}
	}
	selected := make([]string, 0, len(unique))
	for candidateID := range unique {
		selected = append(selected, candidateID)
	}
	if len(selected) > 0 {
		var validCount int
		if err := s.db.QueryRow(ctx, `
			select count(*) from source_discovery_candidates
			where session_id=$1 and id=any($2::text[]) and status='discovered'
		`, sessionID, selected).Scan(&validCount); err != nil {
			return Session{}, err
		}
		if validCount != len(selected) {
			return Session{}, ErrCandidate
		}
	}
	if _, err := s.db.Exec(ctx, `
		update source_discovery_candidates set selected=false,updated_at=now() where session_id=$1
	`, sessionID); err != nil {
		return Session{}, err
	}
	if len(selected) > 0 {
		if _, err := s.db.Exec(ctx, `
			update source_discovery_candidates set selected=true,updated_at=now()
			where session_id=$1 and id=any($2::text[])
		`, sessionID, selected); err != nil {
			return Session{}, err
		}
	}
	if err := realtime.NotifySourceDiscovery(ctx, s.db, sessionID); err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, sessionID)
}

func (s *Store) BeginCandidateImport(ctx context.Context, sessionID, candidateID string) (CandidateImport, error) {
	var candidate CandidateImport
	err := s.db.QueryRow(ctx, `
		update source_discovery_candidates c
		set status='importing',import_error_code=null,updated_at=now()
		from source_discovery_sessions s
		where c.id=$2 and c.session_id=$1 and c.session_id=s.id and c.selected=true
		  and c.status='discovered' and s.status='ready'
		returning c.id,s.notebook_id,c.title,c.canonical_url
	`, sessionID, candidateID).Scan(&candidate.CandidateID, &candidate.NotebookID, &candidate.Title, &candidate.URL)
	if errors.Is(err, pgx.ErrNoRows) {
		return CandidateImport{}, ErrCandidate
	}
	if err != nil {
		return CandidateImport{}, err
	}
	if err := realtime.NotifySourceDiscovery(ctx, s.db, sessionID); err != nil {
		return CandidateImport{}, err
	}
	return candidate, nil
}

func (s *Store) CompleteCandidateImport(ctx context.Context, sessionID, candidateID, sourceID string) error {
	result, err := s.db.Exec(ctx, `
		update source_discovery_candidates
		set status='imported',source_id=$3,import_error_code=null,updated_at=now()
		where session_id=$1 and id=$2 and status='importing'
	`, sessionID, candidateID, sourceID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrState
	}
	return realtime.NotifySourceDiscovery(ctx, s.db, sessionID)
}

func (s *Store) DropCandidateImport(ctx context.Context, sessionID, candidateID string) error {
	result, err := s.db.Exec(ctx, `
		delete from source_discovery_candidates
		where session_id=$1 and id=$2 and status='importing'
	`, sessionID, candidateID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrState
	}
	return realtime.NotifySourceDiscovery(ctx, s.db, sessionID)
}

func (s *Store) CompleteSearch(ctx context.Context, command CompleteSearchCommand) error {
	if strings.TrimSpace(command.SessionID) == "" || strings.TrimSpace(command.JobID) == "" || strings.TrimSpace(command.LeaseToken) == "" {
		return ErrInvalid
	}
	var leasedSessionID, notebookID string
	if err := s.db.QueryRow(ctx, `
		select j.session_id,s.notebook_id
		from source_discovery_jobs j
		join source_discovery_sessions s on s.id=j.session_id
		where j.id=$1 and j.session_id=$2 and j.status='running'
		  and j.lease_token=$3::uuid and j.lease_expires_at > now() and s.status='searching'
		for update of j,s
	`, command.JobID, command.SessionID, command.LeaseToken).Scan(&leasedSessionID, &notebookID); errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	} else if err != nil {
		return err
	}
	canonical := canonicalCandidates(command.Candidates)

	result, err := s.db.Exec(ctx, `
		update source_discovery_sessions
		set status='ready',summary=nullif(trim($2),''),error_code=null,completed_at=now(),updated_at=now()
		where id=$1 and status='searching'
	`, command.SessionID, command.Summary)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err := s.db.Exec(ctx, `delete from source_discovery_candidates where session_id=$1`, command.SessionID); err != nil {
		return err
	}
	if err := s.insertCandidates(ctx, command.SessionID, notebookID, canonical); err != nil {
		return err
	}
	result, err = s.db.Exec(ctx, `
		update source_discovery_jobs
		set status='succeeded',lease_token=null,lease_expires_at=null,last_error_code=null,updated_at=now()
		where id=$1 and session_id=$2 and status='running' and lease_token=$3::uuid
	`, command.JobID, command.SessionID, command.LeaseToken)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return realtime.NotifySourceDiscovery(ctx, s.db, command.SessionID)
}

func (s *Store) CompleteResearchSession(ctx context.Context, researchRunID, summary string, candidates []DiscoveredCandidate) (string, error) {
	if strings.TrimSpace(researchRunID) == "" {
		return "", ErrInvalid
	}
	var sessionID, notebookID, query string
	if err := s.db.QueryRow(ctx, `
		select id,notebook_id,query from source_discovery_sessions
		where research_run_id=$1 and origin='research_agent' and status='searching'
		for update
	`, researchRunID).Scan(&sessionID, &notebookID, &query); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrState
	} else if err != nil {
		return "", err
	}
	canonical := canonicalCandidates(candidates)
	if strings.TrimSpace(summary) == "" {
		summary = SummaryForQuery(query)
	}
	if _, err := s.db.Exec(ctx, `
		update source_discovery_sessions
		set status='ready',summary=nullif(trim($2),''),error_code=null,completed_at=now(),updated_at=now()
		where id=$1
	`, sessionID, summary); err != nil {
		return "", err
	}
	if _, err := s.db.Exec(ctx, `delete from source_discovery_candidates where session_id=$1`, sessionID); err != nil {
		return "", err
	}
	if err := s.insertCandidates(ctx, sessionID, notebookID, canonical); err != nil {
		return "", err
	}
	if err := realtime.NotifySourceDiscovery(ctx, s.db, sessionID); err != nil {
		return "", err
	}
	return sessionID, nil
}

func (s *Store) insertCandidates(ctx context.Context, sessionID, notebookID string, candidates []DiscoveredCandidate) error {
	for ordinal, candidate := range candidates {
		identity, err := source.CanonicalURLIdentity(candidate.URL)
		if err != nil {
			return err
		}
		var existingSourceID *string
		var sourceID string
		err = s.db.QueryRow(ctx, `
			select id from source_sources
			where notebook_id=$1 and input_kind='url'
			  and (origin_url_identity=$2 or final_url_identity=$2)
			order by created_at,id limit 1
		`, notebookID, identity).Scan(&sourceID)
		if err == nil {
			existingSourceID = &sourceID
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		selected := existingSourceID == nil
		status := CandidateDiscovered
		if existingSourceID != nil {
			status = CandidateImported
		}
		if _, err := s.db.Exec(ctx, `
			insert into source_discovery_candidates(
				id,session_id,ordinal,title,canonical_url,display_url,snippet,favicon_ref,selected,status,source_id
			) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		`, candidate.ID, sessionID, ordinal, strings.TrimSpace(candidate.Title), candidate.URL,
			strings.TrimSpace(candidate.DisplayURL), strings.TrimSpace(candidate.Snippet), candidate.FaviconRef,
			selected, status, existingSourceID); err != nil {
			return err
		}
	}
	return nil
}

func SummaryForQuery(query string) string {
	query = truncateSummaryQuery(strings.TrimSpace(query), 500)
	for _, current := range query {
		if unicode.Is(unicode.Han, current) {
			return "以下是与“" + query + "”相关的网页资料，请选择需要导入的内容。"
		}
	}
	return "Relevant web material for “" + query + "”. Select what you want to import."
}

func truncateSummaryQuery(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

func (s *Store) FailResearchSession(ctx context.Context, researchRunID, errorCode string) error {
	if strings.TrimSpace(researchRunID) == "" || strings.TrimSpace(errorCode) == "" {
		return ErrInvalid
	}
	var sessionID string
	err := s.db.QueryRow(ctx, `
		update source_discovery_sessions
		set status='failed',error_code=$2,completed_at=now(),updated_at=now()
		where research_run_id=$1 and origin='research_agent' and status='searching'
		returning id
	`, researchRunID, errorCode).Scan(&sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrState
	}
	if err != nil {
		return err
	}
	return realtime.NotifySourceDiscovery(ctx, s.db, sessionID)
}

func (s *Store) RetryFailedSession(ctx context.Context, sessionID, jobID string) (Session, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(jobID) == "" {
		return Session{}, ErrInvalid
	}
	var notebookID string
	if err := s.db.QueryRow(ctx, `select notebook_id from source_discovery_sessions where id=$1 for update`, sessionID).Scan(&notebookID); errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	} else if err != nil {
		return Session{}, err
	}
	if err := s.requireMaintain(ctx, notebookID); err != nil {
		return Session{}, err
	}
	result, err := s.db.Exec(ctx, `
		update source_discovery_sessions
		set status='searching',summary=null,error_code=null,completed_at=null,updated_at=now()
		where id=$1 and status='failed'
	`, sessionID)
	if err != nil {
		return Session{}, err
	}
	if result.RowsAffected() != 1 {
		return Session{}, ErrState
	}
	if _, err := s.db.Exec(ctx, `delete from source_discovery_candidates where session_id=$1`, sessionID); err != nil {
		return Session{}, err
	}
	if _, err := s.db.Exec(ctx, `
		insert into source_discovery_jobs(id,session_id,status,attempt_no,available_at)
		values($1,$2,'queued',0,now())
		on conflict(session_id) do update set status='queued',attempt_no=0,available_at=now(),
			lease_token=null,lease_expires_at=null,last_error_code=null,updated_at=now()
	`, jobID, sessionID); err != nil {
		return Session{}, err
	}
	if err := realtime.NotifySourceDiscovery(ctx, s.db, sessionID); err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, sessionID)
}

func canonicalCandidates(candidates []DiscoveredCandidate) []DiscoveredCandidate {
	canonical := make([]DiscoveredCandidate, 0, 10)
	seen := make(map[string]struct{}, 10)
	for _, candidate := range candidates {
		if len(canonical) == 10 {
			break
		}
		canonicalURL, ok := canonicalURL(candidate.URL)
		if !ok || strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.Title) == "" {
			continue
		}
		if _, duplicate := seen[canonicalURL]; duplicate {
			continue
		}
		seen[canonicalURL] = struct{}{}
		candidate.URL = canonicalURL
		canonical = append(canonical, candidate)
	}
	return canonical
}

func (s *Store) FailSearch(ctx context.Context, command FailSearchCommand) error {
	if command.SessionID == "" || command.JobID == "" || command.LeaseToken == "" || command.ErrorCode == "" {
		return ErrInvalid
	}
	result, err := s.db.Exec(ctx, `
		update source_discovery_sessions s
		set status='failed',error_code=$4,completed_at=now(),updated_at=now()
		from source_discovery_jobs j
		where s.id=$1 and j.id=$2 and j.session_id=s.id and s.status='searching'
		  and j.status='running' and j.lease_token=$3::uuid and j.lease_expires_at > now()
	`, command.SessionID, command.JobID, command.LeaseToken, command.ErrorCode)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	result, err = s.db.Exec(ctx, `
		update source_discovery_jobs
		set status='failed',lease_token=null,lease_expires_at=null,last_error_code=$4,updated_at=now()
		where id=$2 and session_id=$1 and status='running' and lease_token=$3::uuid
	`, command.SessionID, command.JobID, command.LeaseToken, command.ErrorCode)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return realtime.NotifySourceDiscovery(ctx, s.db, command.SessionID)
}

func canonicalURL(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return "", false
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if port != "" && !((parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443")) {
		host = net.JoinHostPort(host, port)
	}
	parsed.Host = host
	parsed.Fragment = ""
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "fbclid" || lower == "gclid" {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), true
}

func (s *Store) requireMaintain(ctx context.Context, notebookID string) error {
	var canRead, canMaintain bool
	if err := s.db.QueryRow(ctx, `
		select nano_has_notebook_capability($1,'notebook.read'), nano_has_notebook_capability($1,'source.maintain')
	`, notebookID).Scan(&canRead, &canMaintain); err != nil {
		return err
	}
	if !canRead {
		return ErrNotFound
	}
	if !canMaintain {
		return ErrForbidden
	}
	return nil
}
