package store

import (
	"database/sql"
	"strings"
	"time"

	"cyronmanager/internal/secure"
	"cyronmanager/internal/types"
)

func (s *Store) ListProxyProfiles() ([]types.ProxyProfile, error) {
	rows, err := s.db.Query(`SELECT id,name,protocol,host,port,username,password_protected,created_at,updated_at
		FROM proxy_profiles WHERE enabled=1 ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	profiles := make([]types.ProxyProfile, 0)
	for rows.Next() {
		var p types.ProxyProfile
		if err := rows.Scan(&p.ID, &p.Name, &p.Protocol, &p.Host, &p.Port, &p.Username, &p.Password, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

func (s *Store) GetProxyProfile(id string) (types.ProxyProfile, error) {
	var p types.ProxyProfile
	err := s.db.QueryRow(`SELECT id,name,protocol,host,port,username,password_protected,created_at,updated_at
		FROM proxy_profiles WHERE id=? AND enabled=1`, strings.TrimSpace(id)).
		Scan(&p.ID, &p.Name, &p.Protocol, &p.Host, &p.Port, &p.Username, &p.Password, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func (s *Store) SaveProxyProfile(p *types.ProxyProfile) error {
	if p == nil {
		return sql.ErrNoRows
	}
	now := time.Now().UnixMilli()
	if p.CreatedAt == 0 {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	protected, err := protectProxyPassword(p.Password)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO proxy_profiles (
		id,name,protocol,host,port,username,password_protected,enabled,created_at,updated_at
	) VALUES (?,?,?,?,?,?,?,1,?,?)
	ON CONFLICT(id) DO UPDATE SET
		name=excluded.name, protocol=excluded.protocol, host=excluded.host,
		port=excluded.port, username=excluded.username,
		password_protected=excluded.password_protected, enabled=1,
		updated_at=excluded.updated_at`,
		p.ID, p.Name, p.Protocol, p.Host, p.Port, p.Username, protected, p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *Store) DeleteProxyProfile(id string) error {
	_, err := s.db.Exec(`DELETE FROM proxy_profiles WHERE id=?`, strings.TrimSpace(id))
	return err
}

func protectProxyPassword(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if secure.IsProtected(value) {
		return value, nil
	}
	return secure.Protect(value)
}
