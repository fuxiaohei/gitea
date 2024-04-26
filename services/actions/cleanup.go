// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"strconv"
	"strings"
	"time"

	"code.gitea.io/gitea/models/actions"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/storage"
	"code.gitea.io/gitea/modules/util"
)

// Cleanup removes expired actions logs, data and artifacts
func Cleanup(taskCtx context.Context, olderThan time.Duration) error {
	// TODO: clean up expired actions logs

	// clean up expired artifacts
	return CleanupArtifacts(taskCtx)
}

// CleanupArtifacts removes expired add need-deleted artifacts and set records expired status
func CleanupArtifacts(taskCtx context.Context) error {
	if err := cleanExpiredArtifacts(taskCtx); err != nil {
		return err
	}
	if err := cleanNeedDeleteArtifacts(taskCtx); err != nil {
		return err
	}
	return cleanArtifactTemps(taskCtx)
}

func cleanExpiredArtifacts(taskCtx context.Context) error {
	artifacts, err := actions.ListNeedExpiredArtifacts(taskCtx)
	if err != nil {
		return err
	}
	log.Info("Found %d expired artifacts", len(artifacts))
	for _, artifact := range artifacts {
		if err := actions.SetArtifactExpired(taskCtx, artifact.ID); err != nil {
			log.Error("Cannot set artifact %d expired: %v", artifact.ID, err)
			continue
		}
		if err := storage.ActionsArtifacts.Delete(artifact.StoragePath); err != nil {
			log.Error("Cannot delete artifact %d: %v", artifact.ID, err)
			continue
		}
		log.Info("Artifact %d set expired", artifact.ID)
	}
	return nil
}

// deleteArtifactBatchSize is the batch size of deleting artifacts
const deleteArtifactBatchSize = 100

func cleanNeedDeleteArtifacts(taskCtx context.Context) error {
	for {
		artifacts, err := actions.ListPendingDeleteArtifacts(taskCtx, deleteArtifactBatchSize)
		if err != nil {
			return err
		}
		log.Info("Found %d artifacts pending deletion", len(artifacts))
		for _, artifact := range artifacts {
			if err := actions.SetArtifactDeleted(taskCtx, artifact.ID); err != nil {
				log.Error("Cannot set artifact %d deleted: %v", artifact.ID, err)
				continue
			}
			if err := storage.ActionsArtifacts.Delete(artifact.StoragePath); err != nil {
				log.Error("Cannot delete artifact %d: %v", artifact.ID, err)
				continue
			}
			log.Info("Artifact %d set deleted", artifact.ID)
		}
		if len(artifacts) < deleteArtifactBatchSize {
			log.Debug("No more artifacts pending deletion")
			break
		}
	}
	return nil
}

func cleanArtifactTemps(taskCtx context.Context) error {
	storage.ActionsArtifacts.IterateObjects("", func(path string, obj storage.Object) error {
		if !strings.HasPrefix(path, "tmp") {
			return nil
		}
		topDirName := strings.Split(path, "/")[0]
		runIDStr := strings.TrimPrefix(topDirName, "tmp")
		runID, _ := strconv.ParseInt(runIDStr, 10, 64)

		run, err := actions.GetRunByID(taskCtx, runID)
		if err != nil {
			// record not found, delete this chunk
			if strings.Contains(err.Error(), util.ErrNotExist.Error()) {
				log.Warn("Run %d not found, clean up: %s", runID, path)
				if err := storage.ActionsArtifacts.Delete(path); err != nil {
					log.Warn("Cannot delete %s: %v", path, err)
					return err
				}
				return storage.ActionsArtifacts.Delete(topDirName)
			}
			log.Warn("Cannot get run %d: %v", runID, err)
			return nil
		}

		if run.Status == actions.StatusWaiting || run.Status == actions.StatusRunning {
			log.Debug("Run %d is still running, skip cleaning", runID)
			return nil
		}
		log.Info("Run %d is not running, clean up: %s", path)
		if err := storage.ActionsArtifacts.Delete(path); err != nil {
			log.Warn("Cannot delete %s: %v", path, err)
			return err
		}
		return storage.ActionsArtifacts.Delete(topDirName)
	})
	return nil
}
