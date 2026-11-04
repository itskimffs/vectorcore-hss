// Added by itskimffs (2026-11), see NOTICE: Sh repository data.
// This file is part of vectorcore-hss, licensed under the Apache License,
// Version 2.0 (see LICENSE).

package postgres

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/svinson1121/vectorcore-hss/internal/models"
	"github.com/svinson1121/vectorcore-hss/internal/repository"
)

// GetShRepositoryData returns the repository data stored for an IMS subscriber
// under the given Service-Indications. Indications with nothing stored are left
// out.
func (s *Store) GetShRepositoryData(ctx context.Context, imsSubscriberID int, serviceIndications []string) ([]models.ShRepositoryData, error) {
	var rows []models.ShRepositoryData
	err := s.db.WithContext(ctx).
		Where("ims_subscriber_id = ? AND service_indication IN ?", imsSubscriberID, serviceIndications).
		Find(&rows).Error
	return rows, err
}

// UpdateShRepositoryData changes one subscriber's repository data for one
// Service-Indication in a transaction that holds the row. decide gets the stored
// data, or nil when there is none, and returns what to store: nil removes the
// data. An error from decide leaves the data unchanged and is returned as it is.
// repository.ErrConflict means that another writer created the same data at the
// same time.
func (s *Store) UpdateShRepositoryData(ctx context.Context, imsSubscriberID int, serviceIndication string,
	decide func(current *models.ShRepositoryData) (*models.ShRepositoryData, error)) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current models.ShRepositoryData
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("ims_subscriber_id = ? AND service_indication = ?", imsSubscriberID, serviceIndication).
			First(&current).Error
		found := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var cur *models.ShRepositoryData
		if found {
			cur = &current
		}
		next, err := decide(cur)
		if err != nil {
			return err
		}
		switch {
		case next == nil && found:
			return tx.Delete(&current).Error
		case next == nil:
			return nil
		case found:
			return tx.Model(&current).Updates(map[string]any{
				"sequence_number": next.SequenceNumber,
				"service_data":    next.ServiceData,
				"last_modified":   time.Now().UTC(),
			}).Error
		default:
			row := models.ShRepositoryData{
				IMSSubscriberID:   imsSubscriberID,
				ServiceIndication: serviceIndication,
				SequenceNumber:    next.SequenceNumber,
				ServiceData:       next.ServiceData,
				LastModified:      time.Now().UTC(),
			}
			if err := tx.Create(&row).Error; err != nil {
				if errors.Is(err, gorm.ErrDuplicatedKey) || s.shRepositoryDataExists(ctx, imsSubscriberID, serviceIndication) {
					return repository.ErrConflict
				}
				return err
			}
			return nil
		}
	})
	return err
}

// shRepositoryDataExists looks outside the failed transaction, which a failed
// insert has aborted in PostgreSQL.
func (s *Store) shRepositoryDataExists(ctx context.Context, imsSubscriberID int, serviceIndication string) bool {
	var n int64
	s.db.WithContext(ctx).Model(&models.ShRepositoryData{}).
		Where("ims_subscriber_id = ? AND service_indication = ?", imsSubscriberID, serviceIndication).
		Count(&n)
	return n > 0
}
