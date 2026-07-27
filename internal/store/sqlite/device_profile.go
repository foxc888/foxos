package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

func (s *Store) RecordDeviceSnapshot(ctx context.Context, observations []domain.DeviceObservation, observedAt time.Time) error {
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	} else {
		observedAt = observedAt.UTC()
	}
	normalized := make(map[string]domain.DeviceObservation, len(observations))
	for _, observation := range observations {
		mac, err := domain.NormalizeMAC(observation.MACAddress)
		if err != nil {
			return err
		}
		observation.MACAddress = mac
		normalized[mac] = observation
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT mac_address,online FROM device_profiles`)
	if err != nil {
		return err
	}
	previous := make(map[string]bool)
	for rows.Next() {
		var mac string
		var online int
		if err := rows.Scan(&mac, &online); err != nil {
			if closeErr := rows.Close(); closeErr != nil {
				return errors.Join(err, closeErr)
			}
			return err
		}
		previous[mac] = online != 0
	}
	if err := rows.Close(); err != nil {
		return err
	}

	stamp := observedAt.Format(time.RFC3339Nano)
	macs := make([]string, 0, len(normalized))
	for mac := range normalized {
		macs = append(macs, mac)
	}
	sort.Strings(macs)
	for _, mac := range macs {
		observation := normalized[mac]
		lastSeen := ""
		if observation.Online {
			lastSeen = stamp
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO device_profiles(mac_address,host_name,ip_address,interface_name,dhcp_server,first_seen,last_seen,online,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(mac_address) DO UPDATE SET
				host_name=excluded.host_name,ip_address=excluded.ip_address,interface_name=excluded.interface_name,
				dhcp_server=excluded.dhcp_server,last_seen=CASE WHEN excluded.online=1 THEN excluded.last_seen ELSE device_profiles.last_seen END,
				online=excluded.online,updated_at=excluded.updated_at
		`, mac, observation.HostName, observation.IPAddress, observation.Interface, observation.DHCPServer, stamp, lastSeen, boolInt(observation.Online), stamp)
		if err != nil {
			return err
		}
		wasOnline, existed := previous[mac]
		if !existed || wasOnline != observation.Online {
			if _, err := tx.ExecContext(ctx, `INSERT INTO device_presence_events(mac_address,online,observed_at) VALUES(?,?,?)`, mac, boolInt(observation.Online), stamp); err != nil {
				return err
			}
		}
	}
	for mac, wasOnline := range previous {
		if _, seen := normalized[mac]; seen || !wasOnline {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_profiles SET online=0,updated_at=? WHERE mac_address=?`, stamp, mac); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_presence_events(mac_address,online,observed_at) VALUES(?,?,?)`, mac, 0, stamp); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeviceProfiles(ctx context.Context) ([]domain.DeviceProfile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT mac_address,alias,tags_json,vendor,host_name,ip_address,interface_name,dhcp_server,first_seen,last_seen,online,updated_at
		FROM device_profiles ORDER BY online DESC, COALESCE(NULLIF(alias,''),NULLIF(host_name,''),mac_address) COLLATE NOCASE
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.DeviceProfile, 0)
	for rows.Next() {
		item, err := scanDeviceProfile(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DeviceProfile(ctx context.Context, macAddress string) (domain.DeviceProfile, error) {
	mac, err := domain.NormalizeMAC(macAddress)
	if err != nil {
		return domain.DeviceProfile{}, err
	}
	return scanDeviceProfile(s.db.QueryRowContext(ctx, `
		SELECT mac_address,alias,tags_json,vendor,host_name,ip_address,interface_name,dhcp_server,first_seen,last_seen,online,updated_at
		FROM device_profiles WHERE mac_address=?
	`, mac))
}

func (s *Store) UpdateDeviceMetadata(ctx context.Context, macAddress, alias, vendor string, tags []string) (domain.DeviceProfile, error) {
	mac, err := domain.NormalizeMAC(macAddress)
	if err != nil {
		return domain.DeviceProfile{}, err
	}
	if err := domain.ValidateDeviceMetadata(alias, vendor, tags); err != nil {
		return domain.DeviceProfile{}, err
	}
	body, err := json.Marshal(tags)
	if err != nil {
		return domain.DeviceProfile{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE device_profiles SET alias=?,vendor=?,tags_json=?,updated_at=? WHERE mac_address=?`, alias, vendor, string(body), time.Now().UTC().Format(time.RFC3339Nano), mac)
	if err != nil {
		return domain.DeviceProfile{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return domain.DeviceProfile{}, err
	}
	if count == 0 {
		return domain.DeviceProfile{}, ErrNotFound
	}
	return s.DeviceProfile(ctx, mac)
}

func (s *Store) DevicePresenceHistory(ctx context.Context, macAddress string, limit int) ([]domain.DevicePresenceEvent, error) {
	mac, err := domain.NormalizeMAC(macAddress)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,mac_address,online,observed_at FROM device_presence_events WHERE mac_address=? ORDER BY observed_at DESC,id DESC LIMIT ?`, mac, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.DevicePresenceEvent, 0)
	for rows.Next() {
		var item domain.DevicePresenceEvent
		var online int
		var observedAt string
		if err := rows.Scan(&item.ID, &item.MACAddress, &online, &observedAt); err != nil {
			return nil, err
		}
		item.Online = online != 0
		item.ObservedAt, _ = time.Parse(time.RFC3339Nano, observedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanDeviceProfile(row interface{ Scan(...any) error }) (domain.DeviceProfile, error) {
	var item domain.DeviceProfile
	var tagsJSON, firstSeen, lastSeen, updatedAt string
	var online int
	if err := row.Scan(&item.MACAddress, &item.Alias, &tagsJSON, &item.Vendor, &item.HostName, &item.IPAddress, &item.Interface, &item.DHCPServer, &firstSeen, &lastSeen, &online, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.DeviceProfile{}, ErrNotFound
		}
		return domain.DeviceProfile{}, err
	}
	if err := json.Unmarshal([]byte(tagsJSON), &item.Tags); err != nil {
		return domain.DeviceProfile{}, fmt.Errorf("decode device tags: %w", err)
	}
	item.FirstSeen, _ = time.Parse(time.RFC3339Nano, firstSeen)
	item.LastSeen, _ = parseOptionalTime(lastSeen)
	item.Online = online != 0
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return item, nil
}
