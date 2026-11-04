// Modified by itskimffs (2026-11), see NOTICE: Sh repository data.
// This file is part of vectorcore-hss, licensed under the Apache License,
// Version 2.0 (see LICENSE).

package sh

import (
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/svinson1121/vectorcore-hss/internal/config"
	"github.com/svinson1121/vectorcore-hss/internal/repository"
	"go.uber.org/zap"
	"strings"
	"sync"
)

// PeerLookup lets Sh send PNR to registered Application Servers.
type PeerLookup interface {
	GetConn(originHost string) (diam.Conn, bool)
}

type Handlers struct {
	store       repository.Repository
	log         *zap.Logger
	originHost  string
	originRealm string
	mcc         string
	mnc         string
	peers       PeerLookup
	// legacyPrefixedMSISDN also accepts the length | NOA | TBCD User-Identity
	// MSISDN form (sh.msisdn_encoding: legacy_prefixed).
	legacyPrefixedMSISDN bool
	// pendingPNR maps Diameter Session-ID to the original outbound request
	// context so PNA answers can be correlated and logged accurately.
	pendingPNR sync.Map
	// repository is the store's repository data, nil if it has none.
	repository RepositoryDataStore
	// repositoryAS is hss.ShRepositoryDataAS, lower case without a trailing dot.
	repositoryAS map[string]bool
}

func NewHandlers(cfg *config.Config, store repository.Repository, log *zap.Logger, peers PeerLookup) *Handlers {
	h := &Handlers{
		store:        store,
		log:          log,
		originHost:   cfg.HSS.OriginHost,
		originRealm:  cfg.HSS.OriginRealm,
		mcc:          cfg.HSS.MCC,
		mnc:          cfg.HSS.MNC,
		peers:        peers,
		repositoryAS: map[string]bool{},

		legacyPrefixedMSISDN: cfg.Sh.MSISDNEncoding == config.MSISDNEncodingLegacyPrefixed,
	}
	if rs, ok := store.(RepositoryDataStore); ok {
		h.repository = rs
	}
	for _, as := range cfg.HSS.ShRepositoryDataAS {
		h.repositoryAS[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(as), "."))] = true
	}
	return h
}
