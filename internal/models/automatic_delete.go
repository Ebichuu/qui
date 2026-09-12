// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/autobrr/qui/internal/dbinterface"
)

var ErrDeleteOwned = errors.New("automatic delete already owns this torrent")

type DeleteIdentity struct {
	Hash    string
	AddedOn int64
}

type AutomaticDeleteIntent struct {
	DeleteIdentity
	InstanceID  int
	OperationID string
	Owner       string
	Action      string
	State       string
	SubmittedAt time.Time
}

// BeginAutomaticDelete commits the entire batch before any network request.
// Unresolved older generations also block, since a delayed qB request addresses
// a hash, not an added-on generation. Confirmed generations remain tombstones.
func (s *RacingStore) BeginAutomaticDelete(ctx context.Context, instanceID int, operationID, owner, action string, candidates []DeleteIdentity) error {
	if instanceID <= 0 || operationID == "" || owner == "" || owner == "official-reclaim" || len(candidates) == 0 || (action != DeleteModeKeepFiles && action != DeleteModeWithFiles) {
		return racingInvalid("automatic delete intent")
	}
	seen := map[string]struct{}{}
	for _, item := range candidates {
		if item.Hash == "" || item.AddedOn <= 0 {
			return racingInvalid("known delete identity required")
		}
		if _, ok := seen[strings.ToLower(item.Hash)]; ok {
			return racingInvalid("duplicate delete hash")
		}
		seen[strings.ToLower(item.Hash)] = struct{}{}
	}
	_, err := s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := s.lockExecution(ctx, tx, nil); err != nil {
			return 0, err
		}
		return 0, s.claimAutomaticDelete(ctx, tx, instanceID, operationID, owner, action, candidates)
	})
	return err
}

// Caller holds the shared execution lock; used by daily and budgeted reclaim claims.
func (s *RacingStore) claimAutomaticDelete(ctx context.Context, tx dbinterface.TxQuerier, instanceID int, operationID, owner, action string, candidates []DeleteIdentity) error {
	for _, item := range candidates {
		var blocked bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automatic_delete_intents WHERE instance_id=? AND LOWER(torrent_hash)=? AND (state<>'confirmed' OR added_on=?))`, instanceID, strings.ToLower(item.Hash), item.AddedOn).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return ErrDeleteOwned
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, item := range candidates {
		if _, err := tx.ExecContext(ctx, `INSERT INTO automatic_delete_intents(instance_id,torrent_hash,added_on,operation_id,owner,action,state,submitted_at,updated_at) VALUES(?,?,?,?,?,?,'submitted',?,?)`, instanceID, item.Hash, item.AddedOn, operationID, owner, action, now, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *RacingStore) AutomaticDeleteOwned(ctx context.Context, instanceID int, item DeleteIdentity) (bool, error) {
	var owned bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automatic_delete_intents WHERE instance_id=? AND LOWER(torrent_hash)=? AND (state<>'confirmed' OR added_on=?))`, instanceID, strings.ToLower(item.Hash), item.AddedOn).Scan(&owned)
	return owned, err
}

func (s *RacingStore) RecordAutomaticDeleteResult(ctx context.Context, operationID string, accepted bool) error {
	state := "unknown"
	if accepted {
		state = "accepted"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE automatic_delete_intents SET state=?,updated_at=? WHERE operation_id=? AND state='submitted'`, state, time.Now().UTC().Format(time.RFC3339Nano), operationID)
	return err
}

func (s *RacingStore) PendingAutomaticDeletes(ctx context.Context, instanceID int) ([]AutomaticDeleteIntent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT torrent_hash,added_on,operation_id,owner,action,state,submitted_at FROM automatic_delete_intents WHERE instance_id=? AND state<>'confirmed'`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AutomaticDeleteIntent{}
	for rows.Next() {
		item := AutomaticDeleteIntent{InstanceID: instanceID}
		var at string
		if err := rows.Scan(&item.Hash, &item.AddedOn, &item.OperationID, &item.Owner, &item.Action, &item.State, &at); err != nil {
			return nil, err
		}
		item.SubmittedAt, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// Confirmation proves task absence only; it never proves physical space release.
func (s *RacingStore) ConfirmAutomaticDelete(ctx context.Context, item AutomaticDeleteIntent, observedAt time.Time) error {
	if !observedAt.After(item.SubmittedAt) {
		return ErrRacingStale
	}
	_, err := s.db.ExecContext(ctx, `UPDATE automatic_delete_intents SET state='confirmed',updated_at=? WHERE instance_id=? AND torrent_hash=? AND added_on=? AND operation_id=? AND state<>'confirmed'`, observedAt.UTC().Format(time.RFC3339Nano), item.InstanceID, item.Hash, item.AddedOn, item.OperationID)
	return err
}
