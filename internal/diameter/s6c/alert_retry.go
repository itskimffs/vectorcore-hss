package s6c

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/svinson1121/vectorcore-hss/internal/models"
	"github.com/svinson1121/vectorcore-hss/internal/repository"
)

// alertRetryBatchSize caps how many MWD records one sweep examines.
const alertRetryBatchSize = 500

// alertRetryMaxShift bounds the backoff exponent so the shift cannot overflow.
const alertRetryMaxShift = 20

// RunAlertRetry re-sends ALR every interval for MWD whose subscriber is
// SMS-registered on an MME but whose alert has not been acknowledged.
//
// ALRs are otherwise only sent on a ULR or NOR. If the SMS-SC was not
// connected at that moment, or never answered, the alert is lost until the
// subscriber's next ULR/NOR — which for a UE that stays attached may be hours
// or days away, while the SMS-SC holds the message in WAIT_EVENT and never
// retries it on its own. The sweep closes that gap.
//
// Only MNRF (not-reachable) records are re-alerted: MCEF (memory-full) records
// still wait for a NOR with UE-memory-available, since SMS registration says
// nothing about memory. Per record, retries back off exponentially from
// interval up to maxInterval, based on AlertAttemptCount. Blocks until ctx is
// done.
func (h *Handlers) RunAlertRetry(ctx context.Context, interval, maxInterval time.Duration) {
	h.log.Info("s6c: ALR retry sweep enabled",
		zap.Duration("interval", interval),
		zap.Duration("max_interval", maxInterval))
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.alertRetrySweep(ctx, time.Now().UTC(), interval, maxInterval)
		}
	}
}

func (h *Handlers) alertRetrySweep(ctx context.Context, now time.Time, interval, maxInterval time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	records, err := h.store.ListMWDDueForAlertRetry(ctx, now.Add(-interval), alertRetryBatchSize)
	if err != nil {
		h.log.Error("s6c: ALR retry sweep MWD lookup failed", zap.Error(err))
		return
	}

	subs := make(map[string]*models.Subscriber)
	var sent, failed, noPeer int
	for _, mwd := range records {
		if !triggerMatchesMWD(AlertTriggerRetry, mwd.MWDStatusFlags) {
			continue
		}
		if !alertRetryDue(mwd, now, interval, maxInterval) {
			continue
		}
		// Checked here rather than left to sendALR so an SMS-SC that stays
		// down produces one summary line per sweep, not a warning per record.
		if _, ok := h.peers.GetConn(mwd.SCOriginHost); !ok {
			noPeer++
			continue
		}

		sub, cached := subs[mwd.IMSI]
		if !cached {
			sub, err = h.store.GetSubscriberByIMSI(ctx, mwd.IMSI)
			if err != nil && err != repository.ErrNotFound {
				h.log.Error("s6c: ALR retry subscriber lookup failed",
					zap.String("imsi", mwd.IMSI), zap.Error(err))
				continue
			}
			subs[mwd.IMSI] = sub // nil when not found
		}
		if !smsRegisteredOnMME(sub) {
			continue
		}

		if h.sendALR(ctx, sub, mwd, AlertTriggerRetry, nil) {
			sent++
		} else {
			failed++
		}
	}

	if sent+failed+noPeer > 0 {
		h.log.Info("s6c: ALR retry sweep",
			zap.Int("candidates", len(records)),
			zap.Int("sent", sent),
			zap.Int("send_failed", failed),
			zap.Int("sms_sc_not_connected", noPeer))
	}
}

// alertRetryDue reports whether mwd's backoff has elapsed: interval after the
// first attempt, doubling per further attempt, capped at maxInterval.
// Never-attempted records are always due.
func alertRetryDue(mwd models.MessageWaitingData, now time.Time, interval, maxInterval time.Duration) bool {
	if mwd.LastAlertAttemptAt == nil {
		return true
	}
	return now.Sub(*mwd.LastAlertAttemptAt) >= alertRetryBackoff(mwd.AlertAttemptCount, interval, maxInterval)
}

func alertRetryBackoff(attempts uint32, interval, maxInterval time.Duration) time.Duration {
	if attempts <= 1 {
		return min(interval, maxInterval)
	}
	shift := min(attempts-1, alertRetryMaxShift)
	backoff := interval << shift
	if backoff <= 0 || backoff > maxInterval {
		return maxInterval
	}
	return backoff
}

func smsRegisteredOnMME(sub *models.Subscriber) bool {
	return sub != nil &&
		sub.MMERegisteredForSMS != nil && *sub.MMERegisteredForSMS &&
		sub.ServingMME != nil && *sub.ServingMME != ""
}
