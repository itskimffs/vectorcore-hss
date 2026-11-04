package api

import (
	"context"
	"errors"
	"strings"
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

func putRepo(s *Server, id int, si, data string, seq int, expected *int) (*RepositoryDataOutput, error) {
	return s.putIMSSubscriberRepositoryData(context.Background(), &RepositoryDataPutInput{
		RepositoryDataKeyInput: RepositoryDataKeyInput{ID: id, ServiceIndication: si},
		Body:                   RepositoryDataPutBody{ServiceData: data, SequenceNumber: seq, ExpectedSequenceNumber: expected},
	})
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var se interface{ GetStatus() int }
	if err == nil {
		return 200
	}
	if !errors.As(err, &se) {
		t.Fatalf("not a status error: %v", err)
	}
	return se.GetStatus()
}

func TestPutIMSSubscriberRepositoryData(t *testing.T) {
	s := newTestServer(t)
	ims := seedRepositoryData(t, s)

	// Change with the number the editor read: stored as given.
	out, err := putRepo(s, ims.IMSSubscriberID, "MMTEL-Services", `<simservs a="1"/>`, 4, intp(3))
	if err != nil || out.Body.SequenceNumber != 4 || out.Body.ServiceData != `<simservs a="1"/>` {
		t.Fatalf("put: %+v %v", out, err)
	}
	// The number read is now stale.
	if st := statusOf(t, func() error { _, err := putRepo(s, ims.IMSSubscriberID, "MMTEL-Services", "<x/>", 5, intp(3)); return err }()); st != 409 {
		t.Fatalf("stale put: %d", st)
	}
	// Any number may be set, also lower, without a check.
	if out, err := putRepo(s, ims.IMSSubscriberID, "MMTEL-Services", "<x/>", 1, nil); err != nil || out.Body.SequenceNumber != 1 {
		t.Fatalf("put without check: %+v %v", out, err)
	}
	// New data: -1 expects none.
	if _, err := putRepo(s, ims.IMSSubscriberID, "NEW", "<y/>", 0, intp(-1)); err != nil {
		t.Fatal(err)
	}
	if st := statusOf(t, func() error { _, err := putRepo(s, ims.IMSSubscriberID, "NEW", "<y/>", 0, intp(-1)); return err }()); st != 409 {
		t.Fatalf("create twice: %d", st)
	}

	for name, c := range map[string]struct {
		id         int
		si, data   string
		wantStatus int
	}{
		"broken XML":         {ims.IMSSubscriberID, "MMTEL-Services", "<simservs>", 422},
		"empty":              {ims.IMSSubscriberID, "MMTEL-Services", "  ", 422},
		"XML declaration":    {ims.IMSSubscriberID, "MMTEL-Services", `<?xml version="1.0"?><x/>`, 422},
		"too large":          {ims.IMSSubscriberID, "MMTEL-Services", "<x>" + strings.Repeat("a", models.MaxShServiceData) + "</x>", 422},
		"bad indication":     {ims.IMSSubscriberID, " MMTEL", "<x/>", 422},
		"unknown subscriber": {9999, "MMTEL-Services", "<x/>", 404},
	} {
		if st := statusOf(t, func() error { _, err := putRepo(s, c.id, c.si, c.data, 1, nil); return err }()); st != c.wantStatus {
			t.Errorf("%s: %d, want %d", name, st, c.wantStatus)
		}
	}
}

func TestDeleteIMSSubscriberRepositoryData(t *testing.T) {
	s := newTestServer(t)
	ims := seedRepositoryData(t, s)
	del := func(si string, expected int) error {
		_, err := s.deleteIMSSubscriberRepositoryData(context.Background(), &RepositoryDataDeleteInput{
			RepositoryDataKeyInput: RepositoryDataKeyInput{ID: ims.IMSSubscriberID, ServiceIndication: si}, ExpectedSequenceNumber: expected})
		return err
	}
	if st := statusOf(t, del("MMTEL-Services", 2)); st != 409 {
		t.Fatalf("stale delete: %d", st)
	}
	if err := del("MMTEL-Services", 3); err != nil {
		t.Fatal(err)
	}
	if st := statusOf(t, del("MMTEL-Services", -1)); st != 404 {
		t.Fatalf("delete again: %d", st)
	}
	out, _ := s.listIMSSubscriberRepositoryData(context.Background(), &IMSSubscriberIDInput{ID: ims.IMSSubscriberID})
	if len(out.Body) != 1 || out.Body[0].ServiceIndication != "A-OTHER" {
		t.Fatalf("left: %+v", out.Body)
	}
}
