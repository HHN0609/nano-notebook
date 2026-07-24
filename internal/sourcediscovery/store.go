package sourcediscovery

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

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

type CandidateImport struct {
	CandidateID string
	NotebookID  string
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
	created.Candidates = []Candidate{}
	return created, nil
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
			where session_id=$1 and id=any($2::text[]) and status in ('discovered','import_failed')
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
	return s.GetSession(ctx, sessionID)
}

func (s *Store) BeginCandidateImport(ctx context.Context, sessionID, candidateID string) (CandidateImport, error) {
	var candidate CandidateImport
	err := s.db.QueryRow(ctx, `
		update source_discovery_candidates c
		set status='importing',import_error_code=null,updated_at=now()
		from source_discovery_sessions s
		where c.id=$2 and c.session_id=$1 and c.session_id=s.id and c.selected=true
		  and c.status in ('discovered','import_failed') and s.status='ready'
		returning c.id,s.notebook_id,c.canonical_url
	`, sessionID, candidateID).Scan(&candidate.CandidateID, &candidate.NotebookID, &candidate.URL)
	if errors.Is(err, pgx.ErrNoRows) {
		return CandidateImport{}, ErrCandidate
	}
	return candidate, err
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
	return nil
}

func (s *Store) FailCandidateImport(ctx context.Context, sessionID, candidateID, errorCode string) error {
	result, err := s.db.Exec(ctx, `
		update source_discovery_candidates
		set status='import_failed',source_id=null,import_error_code=$3,updated_at=now()
		where session_id=$1 and id=$2 and status='importing'
	`, sessionID, candidateID, errorCode)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrState
	}
	return nil
}

func (s *Store) CompleteSearch(ctx context.Context, command CompleteSearchCommand) error {
	if strings.TrimSpace(command.SessionID) == "" || strings.TrimSpace(command.JobID) == "" || strings.TrimSpace(command.LeaseToken) == "" {
		return ErrInvalid
	}
	var leasedSessionID string
	if err := s.db.QueryRow(ctx, `
		select j.session_id
		from source_discovery_jobs j
		join source_discovery_sessions s on s.id=j.session_id
		where j.id=$1 and j.session_id=$2 and j.status='running'
		  and j.lease_token=$3::uuid and j.lease_expires_at > now() and s.status='searching'
		for update of j,s
	`, command.JobID, command.SessionID, command.LeaseToken).Scan(&leasedSessionID); errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	} else if err != nil {
		return err
	}
	canonical := make([]DiscoveredCandidate, 0, 10)
	seen := make(map[string]struct{}, 10)
	for _, candidate := range command.Candidates {
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
	for ordinal, candidate := range canonical {
		if _, err := s.db.Exec(ctx, `
			insert into source_discovery_candidates(
				id,session_id,ordinal,title,canonical_url,display_url,snippet,favicon_ref,selected,status
			) values($1,$2,$3,$4,$5,$6,$7,$8,true,'discovered')
		`, candidate.ID, command.SessionID, ordinal, strings.TrimSpace(candidate.Title), candidate.URL,
			strings.TrimSpace(candidate.DisplayURL), strings.TrimSpace(candidate.Snippet), candidate.FaviconRef); err != nil {
			return err
		}
	}
	_, err = s.db.Exec(ctx, `
		update source_discovery_jobs
		set status='succeeded',lease_token=null,lease_expires_at=null,last_error_code=null,updated_at=now()
		where id=$1 and session_id=$2 and status='running' and lease_token=$3::uuid
	`, command.JobID, command.SessionID, command.LeaseToken)
	return err
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
	return nil
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
