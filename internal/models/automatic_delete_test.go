// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func TestAutomaticDeleteOwnership(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			ctx := t.Context()
			candidates := []models.DeleteIdentity{{Hash: "synthetic-delete", AddedOn: 100}}
			results := make(chan error, 2)
			var wg sync.WaitGroup
			for i := range 2 {
				wg.Go(func() {
					results <- f.store.BeginAutomaticDelete(ctx, f.instance, fmt.Sprintf("op-%d", i), "daily", "delete", candidates)
				})
			}
			wg.Wait()
			close(results)
			success := 0
			for err := range results {
				if err == nil {
					success++
				} else {
					require.ErrorIs(t, err, models.ErrDeleteOwned)
				}
			}
			require.Equal(t, 1, success)
			items, err := f.store.PendingAutomaticDeletes(ctx, f.instance)
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.ErrorIs(t, f.store.BeginAutomaticDelete(ctx, f.instance, "other", "official", "delete", []models.DeleteIdentity{{Hash: "new", AddedOn: 100}, candidates[0]}), models.ErrDeleteOwned)
			rows, err := f.store.PendingAutomaticDeletes(ctx, f.instance)
			require.NoError(t, err)
			require.Len(t, rows, 1, "claim failure must roll back whole batch")
			require.NoError(t, f.store.RecordAutomaticDeleteResult(ctx, items[0].OperationID, false))
			require.ErrorIs(t, f.store.BeginAutomaticDelete(ctx, f.instance, "new-generation", "official", "delete", []models.DeleteIdentity{{Hash: candidates[0].Hash, AddedOn: 200}}), models.ErrDeleteOwned)
			require.ErrorIs(t, f.store.ConfirmAutomaticDelete(ctx, items[0], items[0].SubmittedAt), models.ErrRacingStale)
			require.NoError(t, f.store.ConfirmAutomaticDelete(ctx, items[0], time.Now().Add(time.Second)))
			require.NoError(t, f.store.RecordAutomaticDeleteResult(ctx, items[0].OperationID, true))
			rows, err = f.store.PendingAutomaticDeletes(ctx, f.instance)
			require.NoError(t, err)
			require.Len(t, rows, 1, "unknown result must not become confirmed through absence or a late response")
			require.Equal(t, "unknown", rows[0].State)
			require.ErrorIs(t, f.store.BeginAutomaticDelete(ctx, f.instance, "same-generation", "official", "delete", candidates), models.ErrDeleteOwned)
			require.ErrorIs(t, f.store.BeginAutomaticDelete(ctx, f.instance, "new-generation", "official", "delete", []models.DeleteIdentity{{Hash: candidates[0].Hash, AddedOn: 200}}), models.ErrDeleteOwned)
			require.ErrorIs(t, f.store.BeginAutomaticDelete(ctx, f.instance, "unknown-generation", "daily", "delete", []models.DeleteIdentity{{Hash: "unknown"}}), models.ErrRacingInvalid)
		})
	}
}
