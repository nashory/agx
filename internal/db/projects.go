package db

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

var ErrProjectNotFound = errors.New("project not found")
var ErrProjectAmbiguous = errors.New("ambiguous project name")

type AmbiguousProjectError struct {
	Ref     string
	Matches []Project
}

func (e AmbiguousProjectError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %q. Use path instead:", ErrProjectAmbiguous, e.Ref)
	for _, project := range e.Matches {
		fmt.Fprintf(&b, "\n  %s", project.Path)
	}
	return b.String()
}

func (e AmbiguousProjectError) Unwrap() error {
	return ErrProjectAmbiguous
}

func (s *Store) EnsureProject(path string, defaultAgent *string) (Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Project{}, err
	}
	name := filepath.Base(abs)
	id := uuid.NewString()
	if _, err := s.db.Exec(`
INSERT INTO projects (id, name, path, description, default_agent)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	default_agent = COALESCE(excluded.default_agent, projects.default_agent),
	last_opened = CURRENT_TIMESTAMP
`, id, name, abs, nil, defaultAgent); err != nil {
		return Project{}, err
	}
	return s.GetProjectByPath(abs)
}

func (s *Store) EnsureProjectDetails(path, name string, description, defaultAgent *string) (Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Project{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = filepath.Base(abs)
	}
	id := uuid.NewString()
	if _, err := s.db.Exec(`
INSERT INTO projects (id, name, path, description, default_agent)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	name = excluded.name,
	description = excluded.description,
	default_agent = COALESCE(excluded.default_agent, projects.default_agent),
	last_opened = CURRENT_TIMESTAMP
`, id, name, abs, description, defaultAgent); err != nil {
		return Project{}, err
	}
	return s.GetProjectByPath(abs)
}

func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.db.Query(`
SELECT id, name, path, description, default_agent, last_opened, created_at
FROM projects
ORDER BY last_opened DESC, name ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (s *Store) GetProjectByPath(path string) (Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Project{}, err
	}
	row := s.db.QueryRow(`
SELECT id, name, path, description, default_agent, last_opened, created_at
FROM projects
WHERE path = ?
`, abs)
	return scanProject(row)
}

func (s *Store) GetProject(id string) (Project, error) {
	row := s.db.QueryRow(`
SELECT id, name, path, description, default_agent, last_opened, created_at
FROM projects
WHERE id = ?
`, id)
	return scanProject(row)
}

func (s *Store) ResolveProject(ref string) (Project, error) {
	if filepath.IsAbs(ref) {
		return s.GetProjectByPath(ref)
	}
	rows, err := s.db.Query(`
SELECT id, name, path, description, default_agent, last_opened, created_at
FROM projects
WHERE name = ?
ORDER BY path ASC
LIMIT 2
`, ref)
	if err != nil {
		return Project{}, err
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return Project{}, err
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return Project{}, err
	}
	switch len(projects) {
	case 0:
		return Project{}, ErrProjectNotFound
	case 1:
		return projects[0], nil
	default:
		return Project{}, AmbiguousProjectError{Ref: ref, Matches: projects}
	}
}

func (s *Store) DeleteProject(id string) error {
	result, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get project rows affected: %w", err)
	}
	if n == 0 {
		return ErrProjectNotFound
	}
	return nil
}

func (s *Store) UpdateProjectDefaultAgent(id string, defaultAgent *string) error {
	result, err := s.db.Exec(`
UPDATE projects SET default_agent = ?, last_opened = CURRENT_TIMESTAMP WHERE id = ?
`, defaultAgent, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get project rows affected: %w", err)
	}
	if n == 0 {
		return ErrProjectNotFound
	}
	return nil
}

func (s *Store) UpdateProjectDetails(id, name string, description *string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("project name is required")
	}
	result, err := s.db.Exec(`
UPDATE projects SET name = ?, description = ?, last_opened = CURRENT_TIMESTAMP WHERE id = ?
`, name, description, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get project rows affected: %w", err)
	}
	if n == 0 {
		return ErrProjectNotFound
	}
	return nil
}

func scanProject(scanner interface {
	Scan(dest ...any) error
}) (Project, error) {
	var p Project
	err := scanner.Scan(&p.ID, &p.Name, &p.Path, &p.Description, &p.DefaultAgent, &p.LastOpened, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrProjectNotFound
	}
	return p, err
}
