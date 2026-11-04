package api

import (
	"context"
	"testing"
	"time"

	"github.com/svinson1121/vectorcore-hss/internal/models"
)

func seedRepositoryData(t *testing.T, s *Server) models.IMSSubscriber {
	t.Helper()
	ims := models.IMSSubscriber{MSISDN: "4794444444", IMSI: ptr("001010000000001")}
	mustCreate(t, s.db, &ims)
	other := models.IMSSubscriber{MSISDN: "4795555555", IMSI: ptr("001010000000002")}
	mustCreate(t, s.db, &other)
	for _, r := range []models.ShRepositoryData{
		{IMSSubscriberID: ims.IMSSubscriberID, ServiceIndication: "MMTEL-Services", SequenceNumber: 3, ServiceData: "<simservs/>", LastModified: time.Now().UTC()},
		{IMSSubscriberID: ims.IMSSubscriberID, ServiceIndication: "A-OTHER", SequenceNumber: 0, ServiceData: "<x/>", LastModified: time.Now().UTC()},
		{IMSSubscriberID: other.IMSSubscriberID, ServiceIndication: "MMTEL-Services", SequenceNumber: 1, ServiceData: "<simservs/>", LastModified: time.Now().UTC()},
	} {
		mustCreate(t, s.db, &r)
	}
	return ims
}

func TestListIMSSubscriberRepositoryData(t *testing.T) {
	s := newTestServer(t)
	ims := seedRepositoryData(t, s)

	out, err := s.listIMSSubscriberRepositoryData(context.Background(), &IMSSubscriberIDInput{ID: ims.IMSSubscriberID})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Body) != 2 || out.Body[0].ServiceIndication != "A-OTHER" || out.Body[1].ServiceIndication != "MMTEL-Services" ||
		out.Body[1].SequenceNumber != 3 || out.Body[1].ServiceData != "<simservs/>" {
		t.Fatalf("got %+v", out.Body)
	}

	if _, err := s.listIMSSubscriberRepositoryData(context.Background(), &IMSSubscriberIDInput{ID: 9999}); err == nil {
		t.Fatal("unknown IMS subscriber: no error")
	}
}

func TestDeleteIMSSubscriberDeletesItsRepositoryData(t *testing.T) {
	s := newTestServer(t)
	ims := seedRepositoryData(t, s)

	if _, err := s.deleteIMSSubscriber(context.Background(), &IMSSubscriberIDInput{ID: ims.IMSSubscriberID}); err != nil {
		t.Fatal(err)
	}
	var left []models.ShRepositoryData
	s.db.Find(&left)
	if len(left) != 1 || left[0].IMSSubscriberID == ims.IMSSubscriberID {
		t.Fatalf("left: %+v", left)
	}
}

// A backup carries the repository data, and a restore brings it back.
func TestBackupRestoreRepositoryData(t *testing.T) {
	s := newTestServer(t)
	seedRepositoryData(t, s)

	doc, err := buildBackup(s.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.ShRepositoryData) != 3 {
		t.Fatalf("backup has %d rows", len(doc.ShRepositoryData))
	}
	s.db.Where("1 = 1").Delete(&models.ShRepositoryData{})
	if _, err := applyRestore(s.db, doc); err != nil {
		t.Fatal(err)
	}
	var n int64
	s.db.Model(&models.ShRepositoryData{}).Count(&n)
	if n != 3 {
		t.Fatalf("%d rows after restore", n)
	}
}
