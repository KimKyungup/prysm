package kv

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	stateTrie "github.com/prysmaticlabs/prysm/beacon-chain/state"
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
	"github.com/sirupsen/logrus"
	"go.opencensus.io/trace"
)

// MigrateToCold advances the finalized info in between the cold and hot state sections.
// It moves the recent finalized states from the hot section to the cold section and
// only preserve the ones that's on archived point.
func (s *Store) MigrateToCold(ctx context.Context, fRoot [32]byte) error {
	ctx, span := trace.StartSpan(ctx, "BeaconDB.MigrateToCold")
	defer span.End()

	s.finalizedInfo.lock.RLock()
	oldFSlot := s.finalizedInfo.slot
	s.finalizedInfo.lock.RUnlock()

	fBlock, err := s.Block(ctx, fRoot)
	if err != nil {
		return err
	}
	fSlot := fBlock.Block.Slot
	if oldFSlot > fSlot {
		return nil
	}

	// Start at previous finalized slot, stop at current finalized slot.
	// If the slot is on archived point, save the state of that slot to the DB.
	for slot := oldFSlot; slot < fSlot; slot++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if slot%s.slotsPerArchivedPoint == 0 && slot != 0 {
			cached, exists, err := s.epochBoundaryStateCache.getBySlot(slot)
			if err != nil {
				return fmt.Errorf("could not get epoch boundary state for slot %d", slot)
			}

			var aRoot [32]byte
			var aState *stateTrie.BeaconState

			// When the epoch boundary state is not in cache due to skip slot scenario,
			// we have to regenerate the state which will represent epoch boundary.
			// By finding the highest available block below epoch boundary slot, we
			// generate the state for that block root.
			if exists {
				aRoot = cached.root
				aState = cached.state
			} else {
				blks, err := s.HighestSlotBlocksBelow(ctx, slot)
				if err != nil {
					return err
				}
				// Given the block has been finalized, the db should not have more than one block in a given slot.
				// We should error out when this happens.
				if len(blks) != 1 {
					return errors.New("unknown block")
				}
				missingRoot, err := blks[0].Block.HashTreeRoot()
				if err != nil {
					return err
				}
				aRoot = missingRoot
				// There's no need to generate the state if the state already exists on the DB.
				// We can skip saving the state.
				has, err := s.HasState(ctx, aRoot)
				if err != nil {
					return err
				}
				if !has {
					aState, err = s.StateByRoot(ctx, missingRoot)
					if err != nil {
						return err
					}
				}
			}

			has, err := s.HasState(ctx, aRoot)
			if err != nil {
				return err
			}
			if has {
				// Remove hot state DB root to prevent it gets deleted later when we turn hot state save DB mode off.
				s.saveHotStateDB.lock.Lock()
				roots := s.saveHotStateDB.savedStateRoots
				for i := 0; i < len(roots); i++ {
					if aRoot == roots[i] {
						s.saveHotStateDB.savedStateRoots = append(roots[:i], roots[i+1:]...)
						// There shouldn't be duplicated roots in `savedStateRoots`.
						// Break here is ok.
						break
					}
				}
				s.saveHotStateDB.lock.Unlock()
				continue
			}

			if err := s.SaveState(ctx, aState, aRoot); err != nil {
				return err
			}
			log.WithFields(
				logrus.Fields{
					"slot": aState.Slot(),
					"root": hex.EncodeToString(bytesutil.Trunc(aRoot[:])),
				}).Info("Saved state in DB")
		}
	}

	// Migrate all state summary objects from state summary cache to DB.
	if err := s.SaveStateSummaries(ctx, s.stateSummaryCache.getAll()); err != nil {
		return err
	}
	s.stateSummaryCache.clear()

	// Update finalized info in memory.
	fInfo, ok, err := s.epochBoundaryStateCache.getByRoot(fRoot)
	if err != nil {
		return err
	}
	if ok {
		s.finalizedInfo.root = fRoot
		s.finalizedInfo.state = fInfo.state.Copy()
		s.finalizedInfo.slot = fSlot
	}

	return nil
}
