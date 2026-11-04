// Added by itskimffs (2026-11), see NOTICE: Sh repository data in the API and web UI.
// This file is part of vectorcore-hss, licensed under the Apache License,
// Version 2.0 (see LICENSE).

package api

// Sh repository data from the API: an operator can change, add or remove the
// transparent data that Application Servers keep in the HSS, and set its
// Sequence-Number. The AS that wrote the data still holds the old number, so its
// next Sh PUR gets DIAMETER_ERROR_TRANSPARENT_DATA_OUT_OF_SYNC (5105) and it has
// to read the data again (TS 29.328 §6.1.2). No Sh-Notif is sent.

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/svinson1121/vectorcore-hss/internal/models"
)

type RepositoryDataKeyInput struct {
	ID                int    `path:"id"`
	ServiceIndication string `path:"service_indication" doc:"The Service-Indication, such as MMTEL-Services"`
}

type RepositoryDataPutBody struct {
	ServiceData    string `json:"service_data" doc:"The XML inside the ServiceData element, such as a simservs document. No XML declaration."`
	SequenceNumber int    `json:"sequence_number" minimum:"0" maximum:"65535" doc:"The Sequence-Number to store. The AS must send this number plus one in its next update."`
	// ExpectedSequenceNumber guards against overwriting a change made since
	// the data was read: the request is refused unless the stored number is
	// still this. -1 means the data must not exist yet. Leave it out to skip
	// the check.
	ExpectedSequenceNumber *int `json:"expected_sequence_number,omitempty" minimum:"-1" maximum:"65535" doc:"Refuse with 409 unless the stored Sequence-Number is still this; -1 means the data must not exist yet. Leave out to skip the check."`
}

type RepositoryDataPutInput struct {
	RepositoryDataKeyInput
	Body RepositoryDataPutBody
}

type RepositoryDataDeleteInput struct {
	RepositoryDataKeyInput
	ExpectedSequenceNumber int `query:"expected_sequence_number" default:"-1" minimum:"-1" maximum:"65535" doc:"Refuse with 409 unless the stored Sequence-Number is still this. -1 skips the check."`
}

type RepositoryDataOutput struct{ Body *models.ShRepositoryData }

func registerRepositoryDataRoutes(s *Server, api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "put-ims-subscriber-repository-data", Method: http.MethodPut, Path: "/ims_subscriber/{id}/repository_data/{service_indication}",
		Summary:     "Set Sh repository data of an IMS Subscriber",
		Description: "Creates or replaces the repository data for one Service-Indication, with the given Sequence-Number. The AS that wrote it before gets DIAMETER_ERROR_TRANSPARENT_DATA_OUT_OF_SYNC on its next update and must read it again.",
		Tags:        []string{"IMS Subscriber"}}, s.putIMSSubscriberRepositoryData)
	huma.Register(api, huma.Operation{OperationID: "delete-ims-subscriber-repository-data", Method: http.MethodDelete, Path: "/ims_subscriber/{id}/repository_data/{service_indication}",
		Summary: "Delete Sh repository data of an IMS Subscriber", Tags: []string{"IMS Subscriber"}}, s.deleteIMSSubscriberRepositoryData)
}

func validServiceIndication(si string) error {
	if strings.TrimSpace(si) != si || si == "" || len(si) > 255 {
		return huma.Error422UnprocessableEntity("service_indication must be 1 to 255 characters without leading or trailing spaces")
	}
	return nil
}

// validateServiceData checks that the service data is well-formed XML that
// fits in a ServiceData element.
func validateServiceData(data string) error {
	trimmed := strings.TrimSpace(data)
	switch {
	case trimmed == "":
		return huma.Error422UnprocessableEntity("service_data must not be empty")
	case len(data) > models.MaxShServiceData:
		return huma.Error422UnprocessableEntity(fmt.Sprintf("service_data is larger than %d bytes", models.MaxShServiceData))
	case strings.HasPrefix(trimmed, "<?xml"):
		return huma.Error422UnprocessableEntity("service_data must not include an XML declaration")
	}
	dec := xml.NewDecoder(strings.NewReader("<ServiceData>" + data + "</ServiceData>"))
	for {
		if _, err := dec.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return huma.Error422UnprocessableEntity("service_data is not well-formed XML: " + err.Error())
		}
	}
}

// errStale is a 409 for a write whose expected Sequence-Number no longer holds.
func errStale(current *models.ShRepositoryData) error {
	if current == nil {
		return huma.Error409Conflict("the repository data has been removed since it was read")
	}
	return huma.Error409Conflict(fmt.Sprintf("the repository data has changed since it was read: its Sequence-Number is now %d", current.SequenceNumber))
}

// lockedRepositoryData returns the stored row, or nil, holding it for the
// transaction. It answers 404 for an unknown IMS subscriber.
func lockedRepositoryData(tx *gorm.DB, id int, si string) (*models.ShRepositoryData, error) {
	if err := tx.First(&models.IMSSubscriber{}, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, huma.Error404NotFound("IMS subscriber not found")
		}
		return nil, err
	}
	var row models.ShRepositoryData
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("ims_subscriber_id = ? AND service_indication = ?", id, si).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func apiError(err error) error {
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	return huma.Error500InternalServerError("db error", err)
}

func (s *Server) putIMSSubscriberRepositoryData(ctx context.Context, in *RepositoryDataPutInput) (*RepositoryDataOutput, error) {
	if err := validServiceIndication(in.ServiceIndication); err != nil {
		return nil, err
	}
	if err := validateServiceData(in.Body.ServiceData); err != nil {
		return nil, err
	}
	var out models.ShRepositoryData
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cur, err := lockedRepositoryData(tx, in.ID, in.ServiceIndication)
		if err != nil {
			return err
		}
		if exp := in.Body.ExpectedSequenceNumber; exp != nil {
			if (cur == nil) != (*exp == -1) || (cur != nil && cur.SequenceNumber != *exp) {
				return errStale(cur)
			}
		}
		now := time.Now().UTC()
		if cur == nil {
			out = models.ShRepositoryData{IMSSubscriberID: in.ID, ServiceIndication: in.ServiceIndication,
				SequenceNumber: in.Body.SequenceNumber, ServiceData: in.Body.ServiceData, LastModified: now}
			return tx.Create(&out).Error
		}
		cur.SequenceNumber = in.Body.SequenceNumber
		cur.ServiceData = in.Body.ServiceData
		cur.LastModified = now
		out = *cur
		return tx.Save(&out).Error
	})
	if err != nil {
		return nil, apiError(err)
	}
	return &RepositoryDataOutput{Body: &out}, nil
}

func (s *Server) deleteIMSSubscriberRepositoryData(ctx context.Context, in *RepositoryDataDeleteInput) (*struct{}, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cur, err := lockedRepositoryData(tx, in.ID, in.ServiceIndication)
		if err != nil {
			return err
		}
		if cur == nil {
			return huma.Error404NotFound("no repository data for this Service-Indication")
		}
		if in.ExpectedSequenceNumber >= 0 && cur.SequenceNumber != in.ExpectedSequenceNumber {
			return errStale(cur)
		}
		return tx.Delete(cur).Error
	})
	if err != nil {
		return nil, apiError(err)
	}
	return nil, nil
}
